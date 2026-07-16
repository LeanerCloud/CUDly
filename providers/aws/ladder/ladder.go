package ladder

import (
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
// when the write-side dependencies have not been wired via WithWriteSide.
// It is distinct from common.ErrCommitmentPurchaseNotSupported, which signals
// that this provider can NEVER purchase a given layer type programmatically.
// Here the capability exists; the instance is just missing its write wiring —
// a configuration error at the call site, not a permanent constraint.
var errWriteNotWired = errors.New("write side not wired: wire riPurchaser, spPurchaser, and exchangeRunner via WithWriteSide before calling PurchaseLayer or ReshapeBuffer")

// ErrLadderExecutionDisabled is returned by PurchaseLayer and ReshapeBuffer when
// the ladder has been wired with a disabled write side (ladder_execution_enabled=false
// in global_config). Use errors.Is(err, ErrLadderExecutionDisabled) to distinguish
// this from errWriteNotWired (missing wiring = programming error at the call site).
var ErrLadderExecutionDisabled = errors.New("ladder write side disabled: set ladder_execution_enabled=true in global_config to enable purchases and reshapes")

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

// AWSLadder implements ladder.LadderCapability for AWS: the read side
// (ListCommitments, GetLayerStates, GetUsageBaseline) and the write side
// (PurchaseLayer, ReshapeBuffer).
//
// All five read data-source dependencies are injected via narrow interfaces so
// that unit tests are hermetic (no real AWS calls needed). The caller wires the
// concrete adapters (ec2svc.Client, savingsplans.Client, etc.) at startup.
//
// The write-side dependencies (riPurchase, spPurchase, exchange) are wired via
// WithWriteSide; until then PurchaseLayer and ReshapeBuffer fail loud with
// errWriteNotWired. This keeps read-only wiring (dashboards, analysis) free of
// purchase/exchange infrastructure.
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
	riCoverage  riCoverageSource
	onDemand    onDemandSeriesSource
	utilization utilizationSource
	spCoverage  spCoverageSource    // nil until parallel SP coverage PR (PR 4) lands
	spUtil      spUtilizationSource // nil until parallel SP utilization PR (PR 4) lands
	riPurchase  riPurchaser         // write side; nil until WithWriteSide is called
	spPurchase  spPurchaser         // write side; nil until WithWriteSide is called
	exchange    exchangeRunner      // write side; nil until WithWriteSide is called
	cfg         Config
}

// New constructs an AWSLadder. The five required read-side interfaces must be
// non-nil; spCov and spUtil may be nil (wired later). riCov and odSeries are
// separate single-method interfaces (interface segregation); one concrete
// adapter may satisfy both and be passed for each.
func New(
	cfg Config,
	ris riLister,
	sps spLister,
	riCov riCoverageSource,
	odSeries onDemandSeriesSource,
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
	if riCov == nil {
		return nil, fmt.Errorf("AWSLadder: riCoverageSource must not be nil")
	}
	if odSeries == nil {
		return nil, fmt.Errorf("AWSLadder: onDemandSeriesSource must not be nil")
	}
	if util == nil {
		return nil, fmt.Errorf("AWSLadder: utilizationSource must not be nil")
	}
	return &AWSLadder{
		cfg:         cfg,
		ris:         ris,
		sps:         sps,
		riCoverage:  riCov,
		onDemand:    odSeries,
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

// WithWriteSide wires the write-side dependencies and returns the same
// instance for chaining. All three must be non-nil: a partially wired write
// side would let one write method work while its sibling fails at call time,
// which is harder to diagnose than failing here at construction.
//
// riP purchases EC2 convertible RIs (LayerConvertibleRI); spP purchases
// Savings Plans (LayerEC2InstanceSP and LayerComputeSP); ex runs the
// automated RI exchange flow backing ReshapeBuffer.
func (a *AWSLadder) WithWriteSide(riP riPurchaser, spP spPurchaser, ex exchangeRunner) (*AWSLadder, error) {
	if riP == nil {
		return nil, fmt.Errorf("AWSLadder.WithWriteSide: riPurchaser must not be nil")
	}
	if spP == nil {
		return nil, fmt.Errorf("AWSLadder.WithWriteSide: spPurchaser must not be nil")
	}
	if ex == nil {
		return nil, fmt.Errorf("AWSLadder.WithWriteSide: exchangeRunner must not be nil")
	}
	a.riPurchase = riP
	a.spPurchase = spP
	a.exchange = ex
	return a, nil
}
