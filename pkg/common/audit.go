package common

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// WriteAuditRecord marshals record to a single JSON line and appends it to path.
// Returns an error if RunID is empty or if any I/O step fails.
func WriteAuditRecord(record AuditRecord, path string) error {
	if record.RunID == "" {
		return fmt.Errorf("audit record RunID must not be empty")
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal audit record: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit log %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit record: %w", err)
	}
	return nil
}

// NewAuditRecord constructs an AuditRecord from a Recommendation and a PurchaseResult.
// status must be one of: "success", "error", "skipped" (dry-run), "skipped_covered" (idempotency).
// source is the CUDly surface that triggered the run — copied into the JSONL so CLI
// audit logs can be reconciled against the DB's purchase_history.source column.
func NewAuditRecord(runID string, rec Recommendation, result PurchaseResult, status string, dryRun bool, source string) AuditRecord {
	errMsg := ""
	if result.Error != nil {
		errMsg = result.Error.Error()
	}
	return AuditRecord{
		RunID:             runID,
		Provider:          rec.Provider,
		AccountID:         rec.Account,
		AccountName:       rec.AccountName,
		Region:            rec.Region,
		Service:           string(rec.Service),
		ResourceType:      rec.ResourceType,
		CommitmentType:    rec.CommitmentType,
		Term:              termMonths(rec.Term),
		Count:             rec.Count,
		EstimatedCost:     rec.CommitmentCost,
		EstimatedSavings:  rec.EstimatedSavings,
		CommitmentID:      result.CommitmentID,
		Status:            status,
		ErrorMessage:      errMsg,
		Timestamp:         time.Now().UTC(),
		DryRun:            dryRun,
		RawRecommendation: rec.RawRecommendation,
		Source:            source,
	}
}

// termMonths converts a term string ("1yr", "3yr") to months.
// Returns 0 for unrecognized strings.
func termMonths(t string) int {
	switch t {
	case "1yr":
		return 12
	case "3yr":
		return 36
	default:
		if t != "" {
			log.Printf("warn: unrecognized term string %q, using 0 months", t)
		}
		return 0
	}
}
