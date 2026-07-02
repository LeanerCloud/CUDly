// Package ladder defines the types, interfaces, and plan rendering for the
// commitment-laddering engine. This package contains the public contract only;
// the allocation algorithm and provider implementations live in internal/ and
// providers/ respectively.
package ladder

import (
	"fmt"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// LayerType identifies a commitment layer in the ladder hierarchy.
type LayerType string

const (
	LayerEC2InstanceSP    LayerType = "ec2-instance-sp"
	LayerComputeSP        LayerType = "compute-sp"
	LayerConvertibleRI    LayerType = "convertible-ri"
	LayerAzureReservation LayerType = "azure-reservation"
	LayerAzureSavingsPlan LayerType = "azure-savings-plan"
)

// Validate returns an error when l is not a recognized LayerType.
func (l LayerType) Validate() error {
	switch l {
	case LayerEC2InstanceSP, LayerComputeSP, LayerConvertibleRI,
		LayerAzureReservation, LayerAzureSavingsPlan:
		return nil
	}
	return fmt.Errorf("unknown layer type %q", l)
}

// ParseLayerType converts s into a LayerType, returning a descriptive error
// when s is not a recognized value. Use it to validate external input at the
// boundary instead of casting raw strings.
func ParseLayerType(s string) (LayerType, error) {
	l := LayerType(s)
	if err := l.Validate(); err != nil {
		return "", err
	}
	return l, nil
}

// LayerRole describes the role a layer plays in the ladder allocation.
type LayerRole string

const (
	RoleBase   LayerRole = "base"
	RoleFlex   LayerRole = "flex"
	RoleBuffer LayerRole = "buffer"
)

// Validate returns an error when r is not a recognized LayerRole.
func (r LayerRole) Validate() error {
	switch r {
	case RoleBase, RoleFlex, RoleBuffer:
		return nil
	}
	return fmt.Errorf("unknown layer role %q", r)
}

// ParseLayerRole converts s into a LayerRole, returning a descriptive error
// when s is not a recognized value.
func ParseLayerRole(s string) (LayerRole, error) {
	r := LayerRole(s)
	if err := r.Validate(); err != nil {
		return "", err
	}
	return r, nil
}

// ActionType describes what a PlannedAction will do.
type ActionType string

const (
	ActionPurchase ActionType = "purchase"
	ActionReshape  ActionType = "reshape"
	ActionHold     ActionType = "hold"
)

// Validate returns an error when a is not a recognized ActionType.
func (a ActionType) Validate() error {
	switch a {
	case ActionPurchase, ActionReshape, ActionHold:
		return nil
	}
	return fmt.Errorf("unknown action type %q", a)
}

// ParseActionType converts s into an ActionType, returning a descriptive
// error when s is not a recognized value.
func ParseActionType(s string) (ActionType, error) {
	a := ActionType(s)
	if err := a.Validate(); err != nil {
		return "", err
	}
	return a, nil
}

// LadderMode controls whether ladder runs require human approval before
// executing purchases.
//
//nolint:revive // Ladder* prefix is the spec-mandated public name (issue #1334); matches pkg/exchange's Exchange* convention.
type LadderMode string

const (
	ModeEmailApproval LadderMode = "email_approval"
	ModeAutoApprove   LadderMode = "auto_approve"
)

// Validate returns an error when m is not a recognized LadderMode.
func (m LadderMode) Validate() error {
	switch m {
	case ModeEmailApproval, ModeAutoApprove:
		return nil
	}
	return fmt.Errorf("unknown ladder mode %q", m)
}

// ParseLadderMode converts s into a LadderMode, returning a descriptive
// error when s is not a recognized value.
func ParseLadderMode(s string) (LadderMode, error) {
	m := LadderMode(s)
	if err := m.Validate(); err != nil {
		return "", err
	}
	return m, nil
}

// LadderCadence controls how often the ladder engine runs.
//
//nolint:revive // Ladder* prefix is the spec-mandated public name (issue #1334); matches pkg/exchange's Exchange* convention.
type LadderCadence string

const (
	CadenceDaily  LadderCadence = "daily"
	CadenceWeekly LadderCadence = "weekly"
)

// Validate returns an error when c is not a recognized LadderCadence.
func (c LadderCadence) Validate() error {
	switch c {
	case CadenceDaily, CadenceWeekly:
		return nil
	}
	return fmt.Errorf("unknown ladder cadence %q", c)
}

// ParseLadderCadence converts s into a LadderCadence, returning a
// descriptive error when s is not a recognized value.
func ParseLadderCadence(s string) (LadderCadence, error) {
	c := LadderCadence(s)
	if err := c.Validate(); err != nil {
		return "", err
	}
	return c, nil
}

// LayerSpec describes a layer and the roles it fulfills within the ladder.
// Azure reservations carry both base and buffer roles simultaneously.
type LayerSpec struct {
	Type  LayerType
	Roles []LayerRole
}

// Scope identifies the ladder scope: a specific provider account or
// subscription that the ladder engine operates on.
type Scope struct {
	Provider  common.ProviderType
	AccountID string
}

// Validate checks that the scope names a known provider and a non-empty
// account identifier. Called first by LadderConfig.Validate so a malformed
// scope fails loud before any other configuration check.
func (s Scope) Validate() error {
	switch s.Provider {
	case common.ProviderAWS, common.ProviderAzure, common.ProviderGCP:
		// known provider
	default:
		return fmt.Errorf("unknown provider %q (allowed: %s, %s, %s)",
			s.Provider, common.ProviderAWS, common.ProviderAzure, common.ProviderGCP)
	}
	if s.AccountID == "" {
		return fmt.Errorf("account ID is required")
	}
	return nil
}

// Default configuration constants. Every default is named and documented so
// callers can reference the symbolic name in code rather than bare literals.
const (
	// DefaultTargetCoveragePct is the percentage of on-demand spend to cover
	// with commitments when no explicit target is configured.
	DefaultTargetCoveragePct = 100.0

	// DefaultBufferFraction is the share of the base allocation reserved in
	// short-term or convertible buffer commitments that can be reshaped as
	// usage patterns shift.
	DefaultBufferFraction = 0.10

	// DefaultBaselinePercentile is the usage percentile used to anchor the
	// base commitment layer. A low percentile guards against over-committing
	// on volatile workloads.
	DefaultBaselinePercentile = 5.0

	// DefaultLookbackDays is the historical window (in days) used to compute
	// the usage baseline when none is specified.
	DefaultLookbackDays = 30

	// rampSumEpsilon is the acceptable absolute error when validating that
	// RampStep fractions sum to 1.0. Floating-point arithmetic can introduce
	// small rounding error, so we allow a small tolerance rather than
	// requiring exact equality.
	rampSumEpsilon = 1e-9
)

// RampStep is a single tranche within a ramp schedule.
type RampStep struct {
	// AfterDays is the number of days after the run starts at which this
	// tranche fires. Must be strictly greater than the previous step's
	// AfterDays (ascending order required by RampSchedule.Validate).
	AfterDays int
	// Fraction is the share of the total target allocation committed by this
	// tranche. Must be in (0, 1]; fractions across all steps must sum to 1.0.
	Fraction float64
}

// RampSchedule spreads commitment purchases across time-indexed tranches.
//
// This type mirrors the semantic intent of internal/config/types.go
// RampSchedule (which uses a percent-per-step + interval model) but is
// defined independently because pkg/ and internal/ are separate Go modules
// and pkg/ cannot import internal/. See internal/config/types.go for the
// internal variant.
type RampSchedule struct {
	Steps []RampStep
}

// Validate checks that the ramp schedule is well-formed:
//   - at least one step
//   - AfterDays values are strictly ascending
//   - each fraction is in (0, 1]
//   - fractions sum to 1.0 within rampSumEpsilon
func (r RampSchedule) Validate() error {
	if len(r.Steps) == 0 {
		return fmt.Errorf("ramp schedule has no steps")
	}
	var sum float64
	prev := -1
	for i, s := range r.Steps {
		if s.AfterDays <= prev {
			return fmt.Errorf("ramp step %d: AfterDays %d must be strictly greater than previous %d", i, s.AfterDays, prev)
		}
		prev = s.AfterDays
		if s.Fraction <= 0 || s.Fraction > 1 {
			return fmt.Errorf("ramp step %d: fraction %g must be in (0, 1]", i, s.Fraction)
		}
		sum += s.Fraction
	}
	diff := sum - 1.0
	if diff < -rampSumEpsilon || diff > rampSumEpsilon {
		return fmt.Errorf("ramp step fractions sum to %g, must equal 1.0", sum)
	}
	return nil
}

// LadderConfig holds the full configuration for a single ladder scope run.
//
//nolint:revive // Ladder* prefix is the spec-mandated public name (issue #1334); matches pkg/exchange's Exchange* convention.
type LadderConfig struct {
	Scope   Scope
	Mode    LadderMode
	Cadence LadderCadence
	// MaxHourlyCommitPerRun caps the total hourly commitment delta a single
	// run may purchase. nil means no cap is applied; when present the cap
	// must be positive (validated by Validate).
	MaxHourlyCommitPerRun *float64
	Ramp                  RampSchedule
	TargetCoveragePct     float64
	BufferFraction        float64
	BaselinePercentile    float64
	LookbackDays          int
	// MaxActionsPerRun limits how many PlannedActions the engine may execute
	// per run. Must be > 0.
	MaxActionsPerRun int
}

// Validate checks that all configuration fields are within valid ranges and
// all sub-types are well-formed. It returns a specific, descriptive error on
// any violation -- callers must not silently default away a validation failure.
func (c *LadderConfig) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return fmt.Errorf("scope: %w", err)
	}
	if err := c.validateBaselineBounds(); err != nil {
		return err
	}
	if err := c.Mode.Validate(); err != nil {
		return fmt.Errorf("mode: %w", err)
	}
	if err := c.Cadence.Validate(); err != nil {
		return fmt.Errorf("cadence: %w", err)
	}
	if err := c.Ramp.Validate(); err != nil {
		return fmt.Errorf("ramp: %w", err)
	}
	return c.validateRunLimits()
}

// validateBaselineBounds checks the coverage target, buffer fraction, and
// baseline measurement parameters. Split out of Validate to keep each
// function's cyclomatic complexity within the repo limit.
func (c *LadderConfig) validateBaselineBounds() error {
	if c.TargetCoveragePct <= 0 || c.TargetCoveragePct > 100 {
		return fmt.Errorf("target_coverage_pct %g must be in (0, 100]", c.TargetCoveragePct)
	}
	if c.BufferFraction < 0 || c.BufferFraction >= 1 {
		return fmt.Errorf("buffer_fraction %g must be in [0, 1)", c.BufferFraction)
	}
	if c.BaselinePercentile <= 0 || c.BaselinePercentile > 50 {
		return fmt.Errorf("baseline_percentile %g must be in (0, 50]", c.BaselinePercentile)
	}
	if c.LookbackDays <= 0 {
		return fmt.Errorf("lookback_days %d must be > 0", c.LookbackDays)
	}
	return nil
}

// validateRunLimits checks the per-run spend and action caps.
func (c *LadderConfig) validateRunLimits() error {
	if c.MaxHourlyCommitPerRun != nil && *c.MaxHourlyCommitPerRun <= 0 {
		return fmt.Errorf("max_hourly_commit_per_run %g must be > 0 when set (leave nil for no cap)", *c.MaxHourlyCommitPerRun)
	}
	if c.MaxActionsPerRun <= 0 {
		return fmt.Errorf("max_actions_per_run %d must be > 0", c.MaxActionsPerRun)
	}
	return nil
}

// UsageBaseline is a statistical summary of recent on-demand usage used to
// size the base commitment layer. Every monetary field is a pointer to
// distinguish "absent" from "genuinely zero" (project rule: absent numbers
// are pointers/nil, never 0).
type UsageBaseline struct {
	// LowWaterUSDPerHour is the usage floor derived from the configured
	// percentile (e.g., the 5th percentile of hourly spend).
	LowWaterUSDPerHour *float64
	// StableUSDPerHour is the estimated stable portion after applying the
	// buffer fraction to LowWaterUSDPerHour.
	StableUSDPerHour *float64
	// Series holds the raw hourly values used for computation. It is optional
	// and may be nil when only the summary statistics are needed (e.g., when
	// populating the approval email body).
	Series []float64
	// LookbackDays is the window (in days) over which the baseline was
	// computed.
	LookbackDays int
	// Percentile is the statistical percentile used for LowWaterUSDPerHour.
	Percentile float64
}

// LayerState is a point-in-time snapshot of an existing commitment layer.
// All monetary and percentage fields are pointers to distinguish "not yet
// measured" from "measured zero" (project rule: absent numbers are nil).
type LayerState struct {
	// ExistingUSDPerHour is the total hourly amortized cost of active
	// commitments in this layer.
	ExistingUSDPerHour *float64
	// ExpiringUSDPerHour is the share of ExistingUSDPerHour whose commitments
	// expire within the current run cadence window.
	ExpiringUSDPerHour *float64
	// CoveragePct is the percentage of eligible on-demand spend currently
	// covered by commitments in this layer.
	CoveragePct *float64
	// UtilizationPct is the current utilization percentage of commitments in
	// this layer.
	UtilizationPct *float64
	// Layer identifies which commitment layer this snapshot describes.
	Layer LayerType
}
