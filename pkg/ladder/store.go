package ladder

import (
	"context"
	"fmt"
	"time"
)

// RunStatus is the lifecycle state of a ladder engine run.
type RunStatus string

const (
	RunStatusPlanned          RunStatus = "planned"
	RunStatusAwaitingApproval RunStatus = "awaiting_approval"
	RunStatusApproved         RunStatus = "approved"
	RunStatusExecuting        RunStatus = "executing"
	RunStatusCompleted        RunStatus = "completed"
	RunStatusFailed           RunStatus = "failed"
	RunStatusCancelled        RunStatus = "cancelled" //nolint:misspell // matches existing DB status spelling ("cancelled")
	RunStatusExpired          RunStatus = "expired"
)

// Validate returns an error when s is not a recognized RunStatus.
func (s RunStatus) Validate() error {
	switch s {
	case RunStatusPlanned, RunStatusAwaitingApproval, RunStatusApproved,
		RunStatusExecuting, RunStatusCompleted, RunStatusFailed,
		RunStatusCancelled, RunStatusExpired:
		return nil
	}
	return fmt.Errorf("unknown run status %q", s)
}

// ParseRunStatus converts s into a RunStatus, returning a descriptive error
// when s is not a recognized value.
func ParseRunStatus(s string) (RunStatus, error) {
	st := RunStatus(s)
	if err := st.Validate(); err != nil {
		return "", err
	}
	return st, nil
}

// TrancheStatus is the lifecycle state of a single ramp tranche.
type TrancheStatus string

const (
	TrancheStatusScheduled TrancheStatus = "scheduled"
	TrancheStatusFired     TrancheStatus = "fired"
	TrancheStatusCompleted TrancheStatus = "completed"
	TrancheStatusCancelled TrancheStatus = "cancelled" //nolint:misspell // matches existing DB status spelling ("cancelled")
	TrancheStatusFailed    TrancheStatus = "failed"
)

// Validate returns an error when s is not a recognized TrancheStatus.
func (s TrancheStatus) Validate() error {
	switch s {
	case TrancheStatusScheduled, TrancheStatusFired, TrancheStatusCompleted,
		TrancheStatusCancelled, TrancheStatusFailed:
		return nil
	}
	return fmt.Errorf("unknown tranche status %q", s)
}

// ParseTrancheStatus converts s into a TrancheStatus, returning a
// descriptive error when s is not a recognized value.
func ParseTrancheStatus(s string) (TrancheStatus, error) {
	st := TrancheStatus(s)
	if err := st.Validate(); err != nil {
		return "", err
	}
	return st, nil
}

// RunRecord is the persistent record of a single ladder engine run. IDs are
// strings to remain agnostic to the backing store's key scheme (UUID, ULID,
// etc.).
type RunRecord struct {
	ID          string
	Scope       Scope
	Status      RunStatus
	CreatedAt   time.Time
	CompletedAt *time.Time
	// PlanJSON holds a serialized LadderPlan for audit. Stored as a string
	// rather than an embedded struct so the store interface stays
	// serialization-format-agnostic.
	PlanJSON string
}

// Tranche is one ramp step that has been persisted for scheduled firing.
type Tranche struct {
	// FireAfter is the wall-clock time at or after which this tranche fires.
	FireAfter time.Time
	// FiredAt is nil until the tranche actually fires.
	FiredAt   *time.Time
	ID        string
	RunID     string
	Status    TrancheStatus
	StepIndex int
}

// LadderStore is the storage contract for the ladder engine. The concrete
// implementation lives in internal/ (separate Go module) and is injected
// into the engine at startup; pkg/ defines only the interface.
//
//nolint:revive // Ladder* prefix is the spec-mandated public name (issue #1334); matches pkg/exchange's Exchange* convention.
type LadderStore interface {
	// SaveRun persists a new run record or updates an existing one (upsert by
	// ID).
	SaveRun(ctx context.Context, run *RunRecord) error

	// LatestRunStartedAt returns the CreatedAt timestamp of the most recent
	// run for the given scope, or nil when no run has been recorded yet.
	LatestRunStartedAt(ctx context.Context, scope Scope) (*time.Time, error)

	// SaveTranches persists a batch of tranches for the given run. Callers
	// may call this multiple times (e.g., once per ramp step) and
	// implementations should upsert by tranche ID.
	SaveTranches(ctx context.Context, runID string, tranches []Tranche) error
}
