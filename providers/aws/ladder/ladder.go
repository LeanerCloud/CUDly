package ladder

import (
	"context"
	"errors"
	"fmt"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// DefaultHorizonDays is the number of days ahead used to classify a
// commitment as "expiring soon" in GetLayerStates.ExpiringUSDPerHour.
// Callers that need a different window pass it via Config.HorizonDays.
const DefaultHorizonDays = 30

// DefaultLookbackDays is the number of days used for coverage and
// utilization queries when Config.LookbackDays is zero.
const DefaultLookbackDays = 30

// errWriteNotWired is the sentinel returned by PurchaseLayer and ReshapeBuffer
// until the write-side PR (PR 6) lands. It is distinct from
// common.ErrCommitmentPurchaseNotSupported, which signals that this provider
// can NEVER purchase a given layer type programmatically. Here the capability
// WILL be supported once wired; the error is a clear placeholder, not a
// permanent constraint.
var errWriteNotWired = errors.New("write side not yet wired (PR 6): call sites must not invoke PurchaseLayer or ReshapeBuffer until the write PR is merged")

// Config holds construction-time parameters for AWSLadder.
type Config struct {
	// Region is the AWS region this ladder instance operates on.
	Region string
	// AccountID is the AWS account ID this ladder instance is scoped to.
	AccountID string
	// HorizonDays is the look-ahead window (in days) used to classify a
	// commitment as expiring soon in ExpiringUSDPerHour. When zero,
	// DefaultHorizonDays is applied.
	HorizonDays int
	// LookbackDays is the history window (in days) for coverage and
	// utilization queries. When zero, DefaultLookbackDays is applied.
	LookbackDays int
}

// horizonDays returns the effective horizon, applying the default when unset.
func (c Config) horizonDays() int {
	if c.HorizonDays > 0 {
		return c.HorizonDays
	}
	return DefaultHorizonDays
}

// lookbackDays returns the effective lookback, applying the default when unset.
func (c Config) lookbackDays() int {
	if c.LookbackDays > 0 {
		return c.LookbackDays
	}
	return DefaultLookbackDays
}

// AWSLadder implements ladder.LadderCapability for AWS. It provides the READ
// side (ListCommitments, GetLayerStates, GetUsageBaseline); the write side
// (PurchaseLayer, ReshapeBuffer) is wired in PR 6 and returns an explicit
// not-implemented error until then.
//
// All four data-source dependencies are injected via narrow interfaces so that
// unit tests are hermetic (no real AWS calls needed). The caller wires the
// concrete adapters (ec2svc.Client, savingsplans.Client, etc.) at startup.
//
// SP coverage and utilization (spCoverageSource, spUtilizationSource) may be
// nil; when nil, CoveragePct and UtilizationPct for SP layers are nil, which
// the engine treats as "not yet measured." They are wired when the parallel
// SP coverage PR lands.
//
// Fields are ordered to minimize the GC pointer-scan range (fieldalignment):
// interface fields (all-pointer) come before Config (which has trailing int fields).
type AWSLadder struct {
	ris         riLister
	sps         spLister
	coverage    coverageSource
	utilization utilizationSource
	spCoverage  spCoverageSource    // nil until parallel SP coverage PR (PR 4) lands
	spUtil      spUtilizationSource // nil until parallel SP utilization PR (PR 4) lands
	cfg         Config
}

// New constructs an AWSLadder. All four required interfaces must be non-nil;
// spCoverage and spUtil may be nil (wired later).
func New(
	cfg Config,
	ris riLister,
	sps spLister,
	cov coverageSource,
	util utilizationSource,
	spCov spCoverageSource,
	spUtil spUtilizationSource,
) (*AWSLadder, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("AWSLadder: Config.Region must not be empty")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("AWSLadder: Config.AccountID must not be empty")
	}
	if ris == nil {
		return nil, fmt.Errorf("AWSLadder: riLister must not be nil")
	}
	if sps == nil {
		return nil, fmt.Errorf("AWSLadder: spLister must not be nil")
	}
	if cov == nil {
		return nil, fmt.Errorf("AWSLadder: coverageSource must not be nil")
	}
	if util == nil {
		return nil, fmt.Errorf("AWSLadder: utilizationSource must not be nil")
	}
	return &AWSLadder{
		cfg:         cfg,
		ris:         ris,
		sps:         sps,
		coverage:    cov,
		utilization: util,
		spCoverage:  spCov,
		spUtil:      spUtil,
	}, nil
}

// Provider returns common.ProviderAWS to identify this implementation.
func (a *AWSLadder) Provider() common.ProviderType {
	return common.ProviderAWS
}

// SupportedLayers returns the three AWS ladder layers:
//   - LayerEC2InstanceSP carries RoleBase (EC2-family-locked SPs for the stable base).
//   - LayerComputeSP carries RoleFlex (compute-wide SPs for flexible coverage).
//   - LayerConvertibleRI carries RoleBuffer (exchangeable RIs for the reshapeable buffer).
//
// Role-cardinality contract: exactly one RoleFlex (ComputeSP), one RoleBase
// (EC2InstanceSP), one RoleBuffer (ConvertibleRI). No multi-role merges on AWS.
func (a *AWSLadder) SupportedLayers() []ladder.LayerSpec {
	return []ladder.LayerSpec{
		{Type: ladder.LayerEC2InstanceSP, Roles: []ladder.LayerRole{ladder.RoleBase}},
		{Type: ladder.LayerComputeSP, Roles: []ladder.LayerRole{ladder.RoleFlex}},
		{Type: ladder.LayerConvertibleRI, Roles: []ladder.LayerRole{ladder.RoleBuffer}},
	}
}

// PurchaseLayer is not yet wired. It returns an explicit placeholder error
// that is NOT common.ErrCommitmentPurchaseNotSupported (which would signal
// permanent inability to purchase). This error signals that the write-side
// wiring is missing; callers must not invoke this method until PR 6 is merged.
//
//nolint:gocritic // hugeParam: Recommendation is large but the LadderCapability interface contract requires value, not pointer
func (a *AWSLadder) PurchaseLayer(_ context.Context, _ ladder.LayerType, _ common.Recommendation, _ common.PurchaseOptions) (common.PurchaseResult, error) {
	return common.PurchaseResult{}, fmt.Errorf("PurchaseLayer: %w", errWriteNotWired)
}

// ReshapeBuffer is not yet wired. It returns the same placeholder error as
// PurchaseLayer; see that method's comment for the rationale.
func (a *AWSLadder) ReshapeBuffer(_ context.Context, _ ladder.Scope, _ ladder.BufferReshapeConfig) (ladder.ReshapeSummary, error) {
	return ladder.ReshapeSummary{}, fmt.Errorf("ReshapeBuffer: %w", errWriteNotWired)
}
