package purchase

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// FinalizeResult summarizes one sweep of FinalizeInFlightRevocations.
// Returned for the scheduled-task handler to log and surface in CloudWatch.
type FinalizeResult struct {
	// Found is the number of purchase_history rows with revocation_in_flight=true
	// and revoked_at IS NULL that the sweep saw.
	Found int
	// Finalized is the number of rows successfully stamped by MarkPurchaseRevoked.
	Finalized int
	// Errored is the number of rows the MarkPurchaseRevoked call failed for.
	Errored int
}

// finalizeRevocationBackoffs are the sleep durations between consecutive
// MarkPurchaseRevoked retry attempts in FinalizeInFlightRevocations.
var finalizeRevocationBackoffs = []time.Duration{
	2 * time.Second,
	6 * time.Second,
}

// FinalizeInFlightRevocations sweeps purchase_history rows with
// revocation_in_flight=true and revoked_at IS NULL and retries
// MarkPurchaseRevoked for each. These rows represent cases where the Azure
// Return API call succeeded but the subsequent DB write failed — the
// finalize_revocations scheduled tick ensures the audit record is eventually
// consistent without requiring the user to retry (which would be rejected by
// Azure as "already returned").
//
// The sweep uses a fixed timestamp of time.Now() at the START of the sweep so
// all rows finalized in one pass share the same revokedAt wall-clock value,
// which makes log correlation easier.
//
// Per-row error handling: rows that fail MarkPurchaseRevoked after retries
// are logged and counted in FinalizeResult.Errored but do not abort the
// sweep — the sweep continues to the next row so a single stuck row does not
// block finalization of all in-flight rows.
func (m *Manager) FinalizeInFlightRevocations(ctx context.Context) (*FinalizeResult, error) {
	rows, err := m.config.GetPurchaseHistoryInFlight(ctx)
	if err != nil {
		return nil, err
	}

	result := &FinalizeResult{Found: len(rows)}
	now := time.Now().UTC()

	for _, record := range rows {
		markErr := m.config.MarkPurchaseRevoked(ctx, record.PurchaseID, now, "direct-api", "", nil, "")
		for attempt, backoff := range finalizeRevocationBackoffs {
			if markErr == nil {
				break
			}
			logging.Warnf("finalize_revocations: MarkPurchaseRevoked attempt %d for %s failed: %v (retrying in %s)",
				attempt+1, record.PurchaseID, markErr, backoff)
			time.Sleep(backoff)
			markErr = m.config.MarkPurchaseRevoked(ctx, record.PurchaseID, now, "direct-api", "", nil, "")
		}
		if markErr != nil {
			logging.Errorf("finalize_revocations: MarkPurchaseRevoked for %s failed after %d attempts: %v",
				record.PurchaseID, len(finalizeRevocationBackoffs)+1, markErr)
			result.Errored++
		} else {
			logging.Infof("finalize_revocations: finalized in-flight revocation for purchase_id=%s", record.PurchaseID)
			result.Finalized++
		}
	}

	return result, nil
}
