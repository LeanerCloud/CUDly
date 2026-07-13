package report

import (
	"encoding/json"
	"os"
	"time"
)

type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
	StatusSkip Status = "SKIP"
)

type CheckResult struct {
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Details   map[string]string `json:"details,omitempty"`
	Name      string            `json:"name"`
	Status    Status            `json:"status"`
	Message   string            `json:"message,omitempty"`
}

type Report struct {
	RunID     string        `json:"run_id"`
	Cloud     string        `json:"cloud"`
	Mode      string        `json:"mode"` // dry-run
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Results   []CheckResult `json:"results"`
}

func (r *Report) Add(res CheckResult) {
	r.Results = append(r.Results, res)
}

func (r *Report) HasFailures() bool {
	for _, rr := range r.Results {
		if rr.Status == StatusFail {
			return true
		}
	}
	return false
}

func (r *Report) WriteJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
