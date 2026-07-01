package purchase

import (
	"context"
	"errors"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// FireResult carries the aggregate outcome of a single FireScheduledDelayedPurchases
// sweep. Callers log these counts and return them to the Lambda scheduler so
// they appear in CloudWatch Logs.
type FireResult struct {
	// Found is the number of status=scheduled rows whose scheduled_execution_at
	// is in the past (SELECT pre-CAS).
	Found int `json:"found"`
	// Fired is the number of rows successfully transitioned from "scheduled" to
	// "approved" and then through executeAndFinalize. Fired <= Found.
	Fired int `json:"fired"`
	// RaceLost is the number of rows where the scheduled->approved CAS was
	// rejected by a concurrent operation (e.g. the user clicked Revoke between
	// the SELECT and the CAS). This is normal and not an error.
	RaceLost int `json:"race_lost"`
	// Errored is the number of rows where the fire attempt failed for a reason
	// other than a CAS race (DB error, provider error). Worth alerting on.
	Errored int `json:"errored"`
}

// FireScheduledDelayedPurchases runs a sweep that fires all purchase_executions
// with status="scheduled" and scheduled_execution_at <= NOW(). For each row:
//
//  1. Atomically transition scheduled -> approved via TransitionExecutionStatus
//     (CAS; skips the row if the revoke handler won the race and flipped it to
//     cancelled first).
//  2. Stamp ApprovedBy = "scheduler" to preserve audit trail.
//  3. Run executeAndFinalize to call the cloud SDK and flip the row to
//     completed/failed.
//
// Safety: idempotent across duplicate invocations (the CAS guard prevents
// double-firing). Per-row error isolation: a failure on row N never blocks
// rows N+1..K. Counts are aggregated in FireResult for the caller to log.
func (m *Manager) FireScheduledDelayedPurchases(ctx context.Context) (*FireResult, error) {
	due, err := m.config.GetScheduledExecutionsDue(ctx)
	if err != nil {
		return nil, fmt.Errorf("fire scheduled purchases: list due rows: %w", err)
	}

	result := &FireResult{Found: len(due)}
	if len(due) == 0 {
		return result, nil
	}

	logging.Infof("FireScheduledDelayedPurchases: found %d row(s) due for execution", len(due))

	for i := range due {
		exec := &due[i]
		if fired, raceLost := m.fireOneDue(ctx, exec); fired {
			result.Fired++
		} else if raceLost {
			result.RaceLost++
		} else {
			result.Errored++
		}
	}

	logging.Infof("FireScheduledDelayedPurchases: found=%d fired=%d race_lost=%d errored=%d",
		result.Found, result.Fired, result.RaceLost, result.Errored)
	return result, nil
}

// fireOneDue attempts to fire a single due scheduled execution. Returns
// (true, false) on success, (false, true) when the CAS was lost to a
// concurrent revoke, or (false, false) on a real error.
func (m *Manager) fireOneDue(ctx context.Context, exec *config.PurchaseExecution) (fired, raceLost bool) {
	// CAS: scheduled -> approved. If this fails with ErrExecutionNotInExpectedStatus
	// the revoke handler already transitioned the row to "canceled" -- that is
	// not an error, just a CAS race loss.
	// Scheduler-initiated fire: no human session UUID, so transitioned_by = NULL.
	updated, err := m.config.TransitionExecutionStatus(ctx, exec.ExecutionID, []string{"scheduled"}, "approved", nil)
	if err != nil {
		if errors.Is(err, config.ErrExecutionNotInExpectedStatus) || errors.Is(err, config.ErrNotFound) {
			logging.Infof("fireOneDue[%s]: CAS lost (execution already transitioned by another actor)", exec.ExecutionID)
			return false, true
		}
		logging.Errorf("fireOneDue[%s]: TransitionExecutionStatus failed: %v", exec.ExecutionID, err)
		return false, false
	}

	// Stamp ApprovedBy for the audit trail before executing.
	actor := "scheduler"
	updated.ApprovedBy = &actor
	if saveErr := m.config.SavePurchaseExecution(ctx, updated); saveErr != nil {
		// Best-effort audit stamp: attribution failure must not block the
		// purchase from firing. Log loudly so the audit gap is visible.
		logging.Errorf("AUDIT GAP: fireOneDue[%s]: failed to stamp approved_by: %v", exec.ExecutionID, saveErr)
	}

	if execErr := m.executeAndFinalize(ctx, updated); execErr != nil {
		logging.Errorf("fireOneDue[%s]: executeAndFinalize failed: %v", exec.ExecutionID, execErr)
		return false, false
	}

	return true, false
}
