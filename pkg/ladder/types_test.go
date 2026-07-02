package ladder

import (
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestLayerTypeValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      LayerType
		wantErr bool
	}{
		{LayerEC2InstanceSP, false},
		{LayerComputeSP, false},
		{LayerConvertibleRI, false},
		{LayerAzureReservation, false},
		{LayerAzureSavingsPlan, false},
		{"unknown-layer", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("LayerType(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("LayerType(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestLayerRoleValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      LayerRole
		wantErr bool
	}{
		{RoleBase, false},
		{RoleFlex, false},
		{RoleBuffer, false},
		{"unknown-role", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("LayerRole(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("LayerRole(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestActionTypeValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      ActionType
		wantErr bool
	}{
		{ActionPurchase, false},
		{ActionReshape, false},
		{ActionHold, false},
		{"unknown-action", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("ActionType(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("ActionType(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestLadderModeValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      LadderMode
		wantErr bool
	}{
		{ModeEmailApproval, false},
		{ModeAutoApprove, false},
		{"unknown-mode", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("LadderMode(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("LadderMode(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestLadderCadenceValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      LadderCadence
		wantErr bool
	}{
		{CadenceDaily, false},
		{CadenceWeekly, false},
		{"monthly", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("LadderCadence(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("LadderCadence(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestRunStatusValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      RunStatus
		wantErr bool
	}{
		{RunStatusPlanned, false},
		{RunStatusAwaitingApproval, false},
		{RunStatusApproved, false},
		{RunStatusExecuting, false},
		{RunStatusCompleted, false},
		{RunStatusFailed, false},
		{RunStatusCancelled, false},
		{RunStatusExpired, false},
		{"unknown-status", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("RunStatus(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("RunStatus(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestTrancheStatusValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      TrancheStatus
		wantErr bool
	}{
		{TrancheStatusScheduled, false},
		{TrancheStatusFired, false},
		{TrancheStatusCompleted, false},
		{TrancheStatusCancelled, false},
		{TrancheStatusFailed, false},
		{"unknown-tranche-status", true},
		{"", true},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr && err == nil {
			t.Errorf("TrancheStatus(%q).Validate() = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("TrancheStatus(%q).Validate() = %v, want nil", c.in, err)
		}
	}
}

func TestParseLayerType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    LayerType
		wantErr bool
	}{
		{"ec2-instance-sp", LayerEC2InstanceSP, false},
		{"compute-sp", LayerComputeSP, false},
		{"convertible-ri", LayerConvertibleRI, false},
		{"azure-reservation", LayerAzureReservation, false},
		{"azure-savings-plan", LayerAzureSavingsPlan, false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseLayerType(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLayerType(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLayerType(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLayerType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseLayerRole(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    LayerRole
		wantErr bool
	}{
		{"base", RoleBase, false},
		{"flex", RoleFlex, false},
		{"buffer", RoleBuffer, false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseLayerRole(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLayerRole(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLayerRole(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLayerRole(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseActionType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    ActionType
		wantErr bool
	}{
		{"purchase", ActionPurchase, false},
		{"reshape", ActionReshape, false},
		{"hold", ActionHold, false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseActionType(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseActionType(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseActionType(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseActionType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseLadderMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    LadderMode
		wantErr bool
	}{
		{"email_approval", ModeEmailApproval, false},
		{"auto_approve", ModeAutoApprove, false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseLadderMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLadderMode(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLadderMode(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLadderMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseLadderCadence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    LadderCadence
		wantErr bool
	}{
		{"daily", CadenceDaily, false},
		{"weekly", CadenceWeekly, false},
		{"monthly", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseLadderCadence(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseLadderCadence(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLadderCadence(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseLadderCadence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseRunStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    RunStatus
		wantErr bool
	}{
		{"planned", RunStatusPlanned, false},
		{"awaiting_approval", RunStatusAwaitingApproval, false},
		{"approved", RunStatusApproved, false},
		{"executing", RunStatusExecuting, false},
		{"completed", RunStatusCompleted, false},
		{"failed", RunStatusFailed, false},
		{"cancelled", RunStatusCancelled, false}, //nolint:misspell // matches existing DB status spelling ("cancelled")
		{"expired", RunStatusExpired, false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseRunStatus(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseRunStatus(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRunStatus(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseRunStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTrancheStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    TrancheStatus
		wantErr bool
	}{
		{"scheduled", TrancheStatusScheduled, false},
		{"fired", TrancheStatusFired, false},
		{"completed", TrancheStatusCompleted, false},
		{"cancelled", TrancheStatusCancelled, false}, //nolint:misspell // matches existing DB status spelling ("cancelled")
		{"failed", TrancheStatusFailed, false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseTrancheStatus(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseTrancheStatus(%q) = %q, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTrancheStatus(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseTrancheStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScopeValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		scope   Scope
		wantErr bool
	}{
		{"valid aws", Scope{Provider: common.ProviderAWS, AccountID: "123456789012"}, false},
		{"valid azure", Scope{Provider: common.ProviderAzure, AccountID: "sub-id"}, false},
		{"valid gcp", Scope{Provider: common.ProviderGCP, AccountID: "my-project"}, false},
		{"unknown provider", Scope{Provider: "oracle", AccountID: "x"}, true},
		{"empty provider", Scope{Provider: "", AccountID: "x"}, true},
		{"empty account", Scope{Provider: common.ProviderAWS, AccountID: ""}, true},
		{"both empty", Scope{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.scope.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// validRamp returns a well-formed two-step RampSchedule for use in table
// tests.
func validRamp() RampSchedule {
	return RampSchedule{Steps: []RampStep{
		{AfterDays: 0, Fraction: 0.5},
		{AfterDays: 7, Fraction: 0.5},
	}}
}

func TestRampScheduleValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		ramp    RampSchedule
		wantErr bool
	}{
		{
			name:    "valid two-step",
			ramp:    validRamp(),
			wantErr: false,
		},
		{
			name:    "single step full fraction",
			ramp:    RampSchedule{Steps: []RampStep{{AfterDays: 0, Fraction: 1.0}}},
			wantErr: false,
		},
		{
			name:    "empty steps",
			ramp:    RampSchedule{},
			wantErr: true,
		},
		{
			name: "unsorted steps",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 7, Fraction: 0.5},
				{AfterDays: 3, Fraction: 0.5},
			}},
			wantErr: true,
		},
		{
			name: "duplicate AfterDays",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 0, Fraction: 0.5},
				{AfterDays: 0, Fraction: 0.5},
			}},
			wantErr: true,
		},
		{
			name: "fraction sum less than 1",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 0, Fraction: 0.4},
				{AfterDays: 7, Fraction: 0.4},
			}},
			wantErr: true,
		},
		{
			name: "fraction sum greater than 1",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 0, Fraction: 0.7},
				{AfterDays: 7, Fraction: 0.7},
			}},
			wantErr: true,
		},
		{
			name: "zero fraction",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 0, Fraction: 0},
			}},
			wantErr: true,
		},
		{
			name: "negative fraction",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 0, Fraction: -0.1},
			}},
			wantErr: true,
		},
		{
			name: "fraction > 1",
			ramp: RampSchedule{Steps: []RampStep{
				{AfterDays: 0, Fraction: 1.5},
			}},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.ramp.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestLadderConfigValidate(t *testing.T) {
	t.Parallel()
	// valid is a fully-populated valid config used as the baseline for each
	// mutation.
	valid := LadderConfig{
		Scope:                         Scope{Provider: common.ProviderAWS, AccountID: "123456789012"},
		TargetCoveragePct:             80,
		BufferFraction:                0.1,
		BaselinePercentile:            5,
		LookbackDays:                  30,
		Mode:                          ModeEmailApproval,
		Cadence:                       CadenceWeekly,
		Ramp:                          validRamp(),
		MaxActionsPerRun:              10,
		BufferUtilizationThresholdPct: DefaultBufferUtilizationThresholdPct,
	}
	cases := []struct {
		mutate  func(c *LadderConfig)
		name    string
		wantErr bool
	}{
		{name: "valid", mutate: func(*LadderConfig) {}, wantErr: false},
		{
			name:    "bad scope provider",
			mutate:  func(c *LadderConfig) { c.Scope.Provider = "oracle" },
			wantErr: true,
		},
		{
			name:    "empty scope account",
			mutate:  func(c *LadderConfig) { c.Scope.AccountID = "" },
			wantErr: true,
		},
		{
			name:    "coverage zero",
			mutate:  func(c *LadderConfig) { c.TargetCoveragePct = 0 },
			wantErr: true,
		},
		{
			name:    "coverage negative",
			mutate:  func(c *LadderConfig) { c.TargetCoveragePct = -1 },
			wantErr: true,
		},
		{
			name:    "coverage above 100",
			mutate:  func(c *LadderConfig) { c.TargetCoveragePct = 100.01 },
			wantErr: true,
		},
		{
			name:    "coverage exactly 100 is valid",
			mutate:  func(c *LadderConfig) { c.TargetCoveragePct = 100 },
			wantErr: false,
		},
		{
			name:    "buffer fraction negative",
			mutate:  func(c *LadderConfig) { c.BufferFraction = -0.1 },
			wantErr: true,
		},
		{
			name:    "buffer fraction exactly 1 is invalid",
			mutate:  func(c *LadderConfig) { c.BufferFraction = 1.0 },
			wantErr: true,
		},
		{
			name:    "buffer fraction 0 is valid",
			mutate:  func(c *LadderConfig) { c.BufferFraction = 0 },
			wantErr: false,
		},
		{
			name:    "percentile zero",
			mutate:  func(c *LadderConfig) { c.BaselinePercentile = 0 },
			wantErr: true,
		},
		{
			name:    "percentile above 50",
			mutate:  func(c *LadderConfig) { c.BaselinePercentile = 50.01 },
			wantErr: true,
		},
		{
			name:    "percentile exactly 50 is valid",
			mutate:  func(c *LadderConfig) { c.BaselinePercentile = 50 },
			wantErr: false,
		},
		{
			name:    "lookback zero",
			mutate:  func(c *LadderConfig) { c.LookbackDays = 0 },
			wantErr: true,
		},
		{
			name:    "lookback negative",
			mutate:  func(c *LadderConfig) { c.LookbackDays = -1 },
			wantErr: true,
		},
		{
			name:    "bad mode",
			mutate:  func(c *LadderConfig) { c.Mode = "unknown_mode" },
			wantErr: true,
		},
		{
			name:    "bad cadence",
			mutate:  func(c *LadderConfig) { c.Cadence = "monthly" },
			wantErr: true,
		},
		{
			name:    "bad ramp (empty steps)",
			mutate:  func(c *LadderConfig) { c.Ramp = RampSchedule{} },
			wantErr: true,
		},
		{
			name:    "hourly commit cap nil is valid (no cap)",
			mutate:  func(c *LadderConfig) { c.MaxHourlyCommitPerRun = nil },
			wantErr: false,
		},
		{
			name: "hourly commit cap positive is valid",
			mutate: func(c *LadderConfig) {
				hourlyCap := 12.5
				c.MaxHourlyCommitPerRun = &hourlyCap
			},
			wantErr: false,
		},
		{
			name: "hourly commit cap zero is invalid",
			mutate: func(c *LadderConfig) {
				hourlyCap := 0.0
				c.MaxHourlyCommitPerRun = &hourlyCap
			},
			wantErr: true,
		},
		{
			name: "hourly commit cap negative is invalid",
			mutate: func(c *LadderConfig) {
				hourlyCap := -1.0
				c.MaxHourlyCommitPerRun = &hourlyCap
			},
			wantErr: true,
		},
		{
			name:    "max actions zero",
			mutate:  func(c *LadderConfig) { c.MaxActionsPerRun = 0 },
			wantErr: true,
		},
		{
			name:    "max actions negative",
			mutate:  func(c *LadderConfig) { c.MaxActionsPerRun = -1 },
			wantErr: true,
		},
		{
			name:    "buffer utilization threshold zero is invalid",
			mutate:  func(c *LadderConfig) { c.BufferUtilizationThresholdPct = 0 },
			wantErr: true,
		},
		{
			name:    "buffer utilization threshold negative is invalid",
			mutate:  func(c *LadderConfig) { c.BufferUtilizationThresholdPct = -1 },
			wantErr: true,
		},
		{
			name:    "buffer utilization threshold above 100 is invalid",
			mutate:  func(c *LadderConfig) { c.BufferUtilizationThresholdPct = 100.01 },
			wantErr: true,
		},
		{
			name:    "buffer utilization threshold exactly 100 is valid",
			mutate:  func(c *LadderConfig) { c.BufferUtilizationThresholdPct = 100 },
			wantErr: false,
		},
		{
			name:    "buffer utilization threshold small positive is valid",
			mutate:  func(c *LadderConfig) { c.BufferUtilizationThresholdPct = 0.1 },
			wantErr: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := valid // shallow copy; all fields are value types or pointers
			c.mutate(&cfg)
			err := cfg.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}
