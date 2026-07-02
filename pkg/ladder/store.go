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

// IsTerminal reports whether s is a terminal run status: RunStatusCompleted,
// RunStatusFailed, RunStatusCancelled, or RunStatusExpired. Terminal runs are
// the only ones allowed to carry a CompletedAt timestamp (see
// RunRecord.Validate).
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled, RunStatusExpired:
		return true
	}
	return false
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

// allowsFiredAt reports whether a tranche in this status may carry a
// non-nil FiredAt timestamp: fired itself, or a post-firing outcome
// (completed, failed). Scheduled and canceled tranches never fired, so
// they must not carry one.
func (s TrancheStatus) allowsFiredAt() bool {
	switch s {
	case TrancheStatusFired, TrancheStatusCompleted, TrancheStatusFailed:
		return true
	}
	return false
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

// Validate checks that the run record is self-consistent: non-empty ID,
// recognized status, and a CompletedAt timestamp only when the status is
// terminal (a run still in flight cannot have completed).
func (r *RunRecord) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("run ID is required")
	}
	if err := r.Status.Validate(); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	if r.CompletedAt != nil && !r.Status.IsTerminal() {
		return fmt.Errorf("completed_at must be nil for non-terminal status %q", r.Status)
	}
	return nil
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

// Validate checks that the tranche is self-consistent: non-empty ID and
// RunID (RunID is the single source of run linkage, see
// LadderStore.SaveTranches), non-negative step index, a set FireAfter
// timestamp, recognized status, and a FiredAt timestamp only when the
// status implies the tranche fired.
func (t *Tranche) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("tranche ID is required")
	}
	if t.RunID == "" {
		return fmt.Errorf("tranche RunID is required")
	}
	if t.StepIndex < 0 {
		return fmt.Errorf("step_index %d must be >= 0", t.StepIndex)
	}
	// A zero FireAfter would make the tranche immediately eligible to fire
	// (year-1 timestamp is always in the past), silently defeating the ramp
	// schedule. Fail loud instead.
	if t.FireAfter.IsZero() {
		return fmt.Errorf("fire_after must be set (zero time would fire immediately)")
	}
	if err := t.Status.Validate(); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	if t.FiredAt != nil && !t.Status.allowsFiredAt() {
		return fmt.Errorf("fired_at must be nil for status %q (tranche has not fired)", t.Status)
	}
	return nil
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

	// SaveTranches persists a batch of tranches. Every tranche must carry a
	// non-empty RunID (Tranche.RunID is the single source of truth for run
	// linkage); implementations persist tranches exactly as given and must
	// not infer linkage from anything else. Callers may call this multiple
	// times (e.g., once per ramp step) and implementations should upsert by
	// tranche ID.
	SaveTranches(ctx context.Context, tranches []Tranche) error
}
