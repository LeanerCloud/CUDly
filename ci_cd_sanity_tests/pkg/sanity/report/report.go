// Package report defines the structured output types for CI/CD sanity-test runs.
package report

import (
	"encoding/json"
	"os"
	"time"
)

// Status is the outcome of a single sanity check.
type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
	StatusSkip Status = "SKIP"
)

// CheckResult records the outcome of one named sanity check, including timing
// and optional key/value details for post-hoc debugging.
type CheckResult struct {
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Details   map[string]string `json:"details,omitempty"`
	Name      string            `json:"name"`
	Status    Status            `json:"status"`
	Message   string            `json:"message,omitempty"`
}

// Report aggregates the results of a full sanity-test run against a single cloud
// provider and is serialized to JSON for upload to the CI artifact store.
type Report struct {
	RunID     string        `json:"run_id"`
	Cloud     string        `json:"cloud"`
	Mode      string        `json:"mode"` // dry-run
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Results   []CheckResult `json:"results"`
}

// Add appends a single check result to the report. Not safe for concurrent
// use; callers running checks in goroutines must serialize Add calls.
func (r *Report) Add(res CheckResult) {
	r.Results = append(r.Results, res)
}

// HasFailures reports whether any recorded check ended in StatusFail. Skips
// and passes do not count as failures.
func (r *Report) HasFailures() bool {
	for _, rr := range r.Results {
		if rr.Status == StatusFail {
			return true
		}
	}
	return false
}

// WriteJSON serializes the report to path with indented JSON and 0600
// permissions so it can be uploaded as a CI artifact.
func (r *Report) WriteJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
