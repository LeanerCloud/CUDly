// Package apiserver implements the CUDly HTTP API server.
package apiserver

import (
	"context"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/scorer"
)

// RunStatus represents the lifecycle state of a run.
type RunStatus string

const (
	RunStatusPendingApproval RunStatus = "pending_approval"
	RunStatusRunning         RunStatus = "running"
	RunStatusCompleted       RunStatus = "completed"
	RunStatusFailed          RunStatus = "failed"
	RunStatusCancelled       RunStatus = "cancelled"
)

// RunRequest is the POST /runs request body.
type RunRequest struct {
	DryRun      bool `json:"dryRun"`
	AutoApprove bool `json:"autoApprove"`
}

// Run represents a single scoring/purchase run.
type Run struct {
	ID          string               `json:"id"`
	Status      RunStatus            `json:"status"`
	CreatedAt   time.Time            `json:"createdAt"`
	CompletedAt *time.Time           `json:"completedAt,omitempty"`
	Request     RunRequest           `json:"request"`
	Result      *scorer.ScoredResult `json:"result,omitempty"`
	AuditPath   string               `json:"auditPath"`
	// unexported coordination fields
	approveCh chan struct{}
	cancelCtx context.CancelFunc
}

// RunStore stores active runs with thread-safe access.
type RunStore struct {
	sync.RWMutex
	runs map[string]*Run
}

// NewRunStore creates an empty RunStore.
func NewRunStore() *RunStore {
	return &RunStore{runs: make(map[string]*Run)}
}

// Get retrieves a run by ID.
func (s *RunStore) Get(id string) (*Run, bool) {
	s.RLock()
	defer s.RUnlock()
	r, ok := s.runs[id]
	return r, ok
}

// Set stores a run.
func (s *RunStore) Set(id string, r *Run) {
	s.Lock()
	defer s.Unlock()
	s.runs[id] = r
}

// AllNonTerminal returns all runs not yet in a terminal state.
func (s *RunStore) AllNonTerminal() []*Run {
	s.RLock()
	defer s.RUnlock()
	var out []*Run
	for _, r := range s.runs {
		if r.Status != RunStatusCompleted && r.Status != RunStatusFailed && r.Status != RunStatusCancelled {
			out = append(out, r)
		}
	}
	return out
}
