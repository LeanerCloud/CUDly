package ladder

import (
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestRunStatusIsTerminal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   RunStatus
		want bool
	}{
		{RunStatusPlanned, false},
		{RunStatusAwaitingApproval, false},
		{RunStatusApproved, false},
		{RunStatusExecuting, false},
		{RunStatusCompleted, true},
		{RunStatusFailed, true},
		{RunStatusCancelled, true},
		{RunStatusExpired, true},
	}
	for _, c := range cases {
		if got := c.in.IsTerminal(); got != c.want {
			t.Errorf("RunStatus(%q).IsTerminal() = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRunRecordValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	valid := RunRecord{
		ID:        "run-1",
		Scope:     Scope{Provider: common.ProviderAWS, AccountID: "123456789012"},
		Status:    RunStatusPlanned,
		CreatedAt: now,
	}
	cases := []struct {
		mutate  func(r *RunRecord)
		name    string
		wantErr bool
	}{
		{name: "valid planned", mutate: func(*RunRecord) {}, wantErr: false},
		{
			name:    "empty ID",
			mutate:  func(r *RunRecord) { r.ID = "" },
			wantErr: true,
		},
		{
			name:    "unknown status",
			mutate:  func(r *RunRecord) { r.Status = "bogus" },
			wantErr: true,
		},
		{
			name: "completed with CompletedAt is valid",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusCompleted
				r.CompletedAt = &now
			},
			wantErr: false,
		},
		{
			name: "failed with CompletedAt is valid",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusFailed
				r.CompletedAt = &now
			},
			wantErr: false,
		},
		{
			name: "RunStatusCancelled with CompletedAt is valid",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusCancelled
				r.CompletedAt = &now
			},
			wantErr: false,
		},
		{
			name: "expired with CompletedAt is valid",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusExpired
				r.CompletedAt = &now
			},
			wantErr: false,
		},
		{
			name: "planned with CompletedAt is invalid",
			mutate: func(r *RunRecord) {
				r.CompletedAt = &now
			},
			wantErr: true,
		},
		{
			name: "executing with CompletedAt is invalid",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusExecuting
				r.CompletedAt = &now
			},
			wantErr: true,
		},
		{
			name: "awaiting_approval with CompletedAt is invalid",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusAwaitingApproval
				r.CompletedAt = &now
			},
			wantErr: true,
		},
		{
			name: "completed with nil CompletedAt is valid (timestamp may lag)",
			mutate: func(r *RunRecord) {
				r.Status = RunStatusCompleted
			},
			wantErr: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := valid
			c.mutate(&rec)
			err := rec.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestTrancheValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	valid := Tranche{
		ID:        "tranche-1",
		RunID:     "run-1",
		StepIndex: 0,
		FireAfter: now,
		Status:    TrancheStatusScheduled,
	}
	cases := []struct {
		mutate  func(tr *Tranche)
		name    string
		wantErr bool
	}{
		{name: "valid scheduled", mutate: func(*Tranche) {}, wantErr: false},
		{
			name:    "empty ID",
			mutate:  func(tr *Tranche) { tr.ID = "" },
			wantErr: true,
		},
		{
			name:    "empty RunID",
			mutate:  func(tr *Tranche) { tr.RunID = "" },
			wantErr: true,
		},
		{
			name:    "negative StepIndex",
			mutate:  func(tr *Tranche) { tr.StepIndex = -1 },
			wantErr: true,
		},
		{
			name:    "unknown status",
			mutate:  func(tr *Tranche) { tr.Status = "bogus" },
			wantErr: true,
		},
		{
			name: "fired with FiredAt is valid",
			mutate: func(tr *Tranche) {
				tr.Status = TrancheStatusFired
				tr.FiredAt = &now
			},
			wantErr: false,
		},
		{
			name: "completed with FiredAt is valid",
			mutate: func(tr *Tranche) {
				tr.Status = TrancheStatusCompleted
				tr.FiredAt = &now
			},
			wantErr: false,
		},
		{
			name: "failed with FiredAt is valid",
			mutate: func(tr *Tranche) {
				tr.Status = TrancheStatusFailed
				tr.FiredAt = &now
			},
			wantErr: false,
		},
		{
			name: "scheduled with FiredAt is invalid",
			mutate: func(tr *Tranche) {
				tr.FiredAt = &now
			},
			wantErr: true,
		},
		{
			name: "TrancheStatusCancelled with FiredAt is invalid",
			mutate: func(tr *Tranche) {
				tr.Status = TrancheStatusCancelled
				tr.FiredAt = &now
			},
			wantErr: true,
		},
		{
			name: "fired with nil FiredAt is valid (timestamp may lag)",
			mutate: func(tr *Tranche) {
				tr.Status = TrancheStatusFired
			},
			wantErr: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := valid
			c.mutate(&tr)
			err := tr.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}
