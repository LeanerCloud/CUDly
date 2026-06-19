package purchase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// stuckStatuses are the in-flight execution statuses that the reaper will
// flip to "failed" when the row has been sitting in one of them for longer
// than the configured reapAfter duration. These are the only intermediate
// states the synchronous executor passes through between the user clicking
// approve and the row landing in a terminal state (completed/failed) —
// pending/notified executions are advanced by the scheduler tick on its
// own cadence, not by us. See ApproveExecution + executeAndFinalize for
// the originating state machine.
//
// Kept as a package-level var (not a const slice) so tests can read it
// without re-typing the literal, while staying immutable in callers.
var stuckStatuses = []string{"approved", "running"}

// failedStatus is the terminal status the reaper sets via the atomic CAS in
// TransitionExecutionStatus. Named as a constant so a future rename (e.g.
// migrating to a typed status enum) shows up at the one call site.
const failedStatus = "failed"

// DefaultReapAfter is the default age at which an approved/running execution
// is considered stuck. The issue (#678) calls for "configurable via env var,
// default 10m" — the threshold is intentionally conservative: the happy-path
// executor completes in <60s; the longest legitimately-slow paths
// (multi-account fan-out, provider rate-limit backoff) settle in <3min. 10
// minutes leaves multiple multiples of headroom so the reaper never fights
// a real executor.
const DefaultReapAfter = 10 * time.Minute

// reapAfterEnvVar is the env var read by ParseReapAfterFromEnv. Centralized
// so the wiring code, the tests, and future ops documentation all reference
// the same name.
const reapAfterEnvVar = "PURCHASE_APPROVED_REAP_AFTER"

// ReapResult summarizes one sweep of ReapStuckExecutions. Returned for the
// scheduled-task handler to log + surface in CloudWatch / metrics.
type ReapResult struct {
	// Found is the number of rows the SELECT returned (i.e. stuck rows the
	// sweep saw — pre-CAS).
	Found int `json:"found"`
	// Reaped is the number of rows that successfully transitioned to
	// "failed" via the atomic CAS. Reaped <= Found; the gap is rows the
	// real executor finished between the SELECT and the CAS (CAS race
	// rejected the reap — correct behavior, not an error).
	Reaped int `json:"reaped"`
	// RaceLost is the number of rows where the CAS rejected the reap
	// because the row's status changed between the SELECT and the
	// transition (the real executor woke up and beat us). Logged at
	// INFO; not surfaced as an error.
	RaceLost int `json:"race_lost"`
	// Errored is the number of rows where the persistence step failed
	// (CAS itself errored or the canonical-error follow-up save failed).
	// These are real ops issues worth surfacing.
	Errored int `json:"errored"`
}

// ParseReapAfterFromEnv reads PURCHASE_APPROVED_REAP_AFTER from the
// environment and parses it via time.ParseDuration. Falls back to
// DefaultReapAfter on either an absent env var, a parse failure, or a
// non-positive duration (0s / negative) — each variant logs a WARN so ops
// can spot a typo without the reaper silently running at the default.
// Never panics: a misconfigured env var must not crash the Lambda's other
// scheduled tasks.
//
// Non-positive values are explicitly rejected: time.ParseDuration accepts
// "0s" and "-5m" as valid, but feeding them into ListStuckExecutions would
// either match every row (0s) or invert the SELECT into "updated_at <
// NOW() + |d|" (negative) and reap fresh executions. The store has its
// own guard (defense-in-depth) but rejecting here keeps the misconfig
// visible in the WARN log rather than as a confusing store error.
func ParseReapAfterFromEnv() time.Duration {
	raw := os.Getenv(reapAfterEnvVar)
	if raw == "" {
		return DefaultReapAfter
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		logging.Warnf("purchase reaper: failed to parse %s=%q as duration: %v — using default %s",
			reapAfterEnvVar, raw, err, DefaultReapAfter)
		return DefaultReapAfter
	}
	if d <= 0 {
		logging.Warnf("purchase reaper: invalid %s=%q (must be > 0) — using default %s",
			reapAfterEnvVar, raw, DefaultReapAfter)
		return DefaultReapAfter
	}
	return d
}

// ReapStuckExecutions runs one sweep that finds purchase_executions stuck in
// approved/running longer than reapAfter and atomically transitions them to
// "failed" via the existing TransitionExecutionStatus CAS. Each successful
// transition also persists a canonical error message so the History UI
// (issue #621) can show why the row was reaped and the operator knows it's
// safe to retry.
//
// Safety properties:
//   - Local-status-only: the reaper never touches provider commitments. If
//     the real executor did get a commitment created on the provider before
//     dying, the row is still flipped to failed locally — the operator's
//     retry hits the idempotency path (#636/#638/#652) which surfaces a
//     duplicate-reservation error and short-circuits cleanly.
//   - CAS-protected: if the real executor wakes up and finishes between the
//     SELECT and the CAS, TransitionExecutionStatus returns an error
//     ("cannot transition from completed/failed"); we log at INFO and move
//     on. The real executor wins the race.
//   - Per-row error-isolation: a failure on row N never blocks rows
//     N+1..K. Counts are aggregated in ReapResult for the caller to log.
func (m *Manager) ReapStuckExecutions(ctx context.Context, reapAfter time.Duration) (*ReapResult, error) {
	stuck, err := m.config.ListStuckExecutions(ctx, stuckStatuses, reapAfter)
	if err != nil {
		return nil, fmt.Errorf("failed to list stuck executions: %w", err)
	}

	result := &ReapResult{Found: len(stuck)}
	if len(stuck) == 0 {
		logging.Debugf("purchase reaper: no stuck executions (threshold %s)", reapAfter)
		return result, nil
	}

	now := time.Now()
	for i := range stuck {
		m.reapOne(ctx, &stuck[i], reapAfter, now, result)
	}

	logging.Infof("purchase reaper sweep complete: found=%d reaped=%d race_lost=%d errored=%d (threshold %s)",
		result.Found, result.Reaped, result.RaceLost, result.Errored, reapAfter)

	return result, nil
}

// reapOne handles a single stuck row: atomic CAS to failed, then a
// best-effort persistence of the canonical error message so History can
// show the operator why the row was reaped. Updates the shared ReapResult
// counters in place. Extracted from ReapStuckExecutions so the per-row
// path stays under the gocyclo threshold.
//
// `now` is passed in (rather than read inline) so age math is consistent
// across all rows in one sweep — useful for log-spam-free re-runs and for
// deterministic tests that inject a fixed clock.
func (m *Manager) reapOne(ctx context.Context, exec *config.PurchaseExecution, reapAfter time.Duration, now time.Time, result *ReapResult) {
	prevStatus := exec.Status

	// Best-effort age estimate. The store does not currently return
	// updated_at on the PurchaseExecution struct (issue #678 may add it
	// later), so we lower-bound the age at reapAfter — the SELECT
	// guarantees updated_at < now - reapAfter, so the real age is at
	// least reapAfter. Rounded to whole minutes for the canonical
	// message; the WARN log carries the same lower bound.
	// `now` is injected (rather than time.Now()) so tests can use a
	// fixed clock and all rows in one sweep share the same reference
	// instant (05-N1).
	age := reapAfter
	ageMinutes := int(age.Round(time.Minute) / time.Minute)
	if ageMinutes < 1 {
		ageMinutes = 1
	}

	logging.Warnf("purchase reaper: reaping execution %s (status=%s, age>=%dm sweep_at=%s)",
		exec.ExecutionID, prevStatus, ageMinutes, now.UTC().Format(time.RFC3339))

	// System-initiated: reaper passes nil so transitioned_by = NULL.
	transitioned, err := m.config.TransitionExecutionStatus(ctx, exec.ExecutionID, stuckStatuses, failedStatus, nil)
	if err != nil {
		// Distinguish CAS race-loss (the real executor finished between
		// our SELECT and CAS, so the row is no longer in
		// approved/running) from a hard DB error (connection dropped,
		// query syntax error, etc.). The store wraps both legitimate
		// race outcomes in sentinel errors so we can use errors.Is
		// rather than brittle string matching:
		//   - ErrExecutionNotInExpectedStatus: row exists but its
		//     status moved out of approved/running before the CAS
		//     (the real executor won the race — expected, log INFO).
		//   - ErrNotFound: row vanished between SELECT and CAS (very
		//     rare — e.g. a manual DELETE; still a race outcome the
		//     reaper has nothing to do about, log INFO).
		// Anything else is a real ops issue (DB outage, etc.) and
		// must bump Errored so it surfaces in metrics/alerts instead
		// of being silently absorbed as "race lost".
		if errors.Is(err, config.ErrExecutionNotInExpectedStatus) || errors.Is(err, config.ErrNotFound) {
			logging.Infof("purchase reaper: CAS race lost for execution %s (status changed from %q before transition): %v",
				exec.ExecutionID, prevStatus, err)
			result.RaceLost++
			return
		}
		logging.Errorf("purchase reaper: transition failed for execution %s (status=%q): %v",
			exec.ExecutionID, prevStatus, err)
		result.Errored++
		return
	}
	if transitioned == nil {
		// Defensive: the store contract returns either (record, nil) or
		// (nil, err). Treat (nil, nil) as a race-lost too — same caller
		// intent, no canonical-error save to attempt.
		logging.Infof("purchase reaper: CAS returned nil row for execution %s — treating as race lost", exec.ExecutionID)
		result.RaceLost++
		return
	}

	// Persist the canonical error message. The CAS already flipped status
	// to "failed"; this follow-up SavePurchaseExecution writes the human-
	// readable error string so History shows why the row was reaped.
	// Best-effort: if this save fails (network blip, etc.) the row is
	// still in "failed" — operator just sees a generic failure without
	// the reaper attribution. Bump Errored so ops can track it.
	transitioned.Error = fmt.Sprintf("reaped after %dm in %s state — executor did not complete; safe to retry",
		ageMinutes, prevStatus)
	if saveErr := m.config.SavePurchaseExecution(ctx, transitioned); saveErr != nil {
		logging.Errorf("purchase reaper: failed to persist canonical error for execution %s (already flipped to failed): %v",
			exec.ExecutionID, saveErr)
		result.Errored++
		// Still count as Reaped — the status flip succeeded, only the
		// error-message annotation failed. Otherwise Reaped would
		// undercount real recoveries.
		result.Reaped++
		return
	}

	result.Reaped++
}
