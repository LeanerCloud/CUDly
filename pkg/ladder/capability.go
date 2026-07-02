package ladder

import (
	"context"
	"math/big"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// LadderCapability is implemented by each cloud provider to give the ladder
// engine a uniform interface for querying commitment state and executing
// purchases. Implementations live in providers/ and are injected into the
// engine at startup.
//
//nolint:revive // Ladder* prefix is the spec-mandated public name (issue #1334); matches pkg/exchange's Exchange* convention.
type LadderCapability interface {
	// Provider returns the cloud provider identifier (common.ProviderAWS,
	// common.ProviderAzure, or common.ProviderGCP).
	Provider() common.ProviderType

	// SupportedLayers returns the commitment layers this provider can fulfill.
	// Each LayerSpec declares both the layer type and the roles it covers.
	SupportedLayers() []LayerSpec

	// ListCommitments returns all active commitments for the given scope.
	ListCommitments(ctx context.Context, scope Scope) ([]common.Commitment, error)

	// GetLayerStates returns a point-in-time snapshot for each supported layer.
	// Layers with no active commitments are represented with nil pointer fields
	// (never coerced to zero).
	GetLayerStates(ctx context.Context, scope Scope) (map[LayerType]LayerState, error)

	// GetUsageBaseline computes a statistical baseline from historical
	// on-demand usage over the given lookback window and percentile.
	GetUsageBaseline(ctx context.Context, scope Scope, lookbackDays int, percentile float64) (UsageBaseline, error)

	// PurchaseLayer buys commitments for the given layer using the provided
	// recommendation. Implementations return an error wrapping
	// common.ErrCommitmentPurchaseNotSupported when the layer cannot be
	// purchased programmatically; callers detect it with
	// errors.Is(err, common.ErrCommitmentPurchaseNotSupported).
	//
	// Precision note: the engine plans amounts as exact *big.Rat values
	// (PlannedAction.AmountUSDPerHour), but this boundary converts them to
	// the float64 cost fields of common.Recommendation. This is a documented
	// precision seam; exact-decimal plumbing through the purchase path is
	// addressed in a later PR.
	PurchaseLayer(ctx context.Context, layer LayerType, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error)

	// ReshapeBuffer exchanges or modifies buffer-layer commitments to improve
	// utilization when it falls below the configured threshold.
	ReshapeBuffer(ctx context.Context, scope Scope, cfg BufferReshapeConfig) (ReshapeSummary, error)
}

// BufferReshapeConfig parameterizes a buffer reshape operation.
type BufferReshapeConfig struct {
	// MaxPaymentPerExchangeUSD caps the payment for a single exchange. nil
	// means no per-exchange cap is applied.
	MaxPaymentPerExchangeUSD *big.Rat
	// MaxPaymentDailyUSD caps the total exchange payments for the current day.
	// nil means no daily cap is applied.
	MaxPaymentDailyUSD *big.Rat
	// UtilizationThresholdPct triggers a reshape when commitment utilization
	// drops below this percentage.
	UtilizationThresholdPct float64
	// LookbackDays is the window used to measure utilization when deciding
	// whether to trigger a reshape.
	LookbackDays int
	// DryRun, when true, simulates the reshape without executing any exchanges.
	DryRun bool
}

// ReshapeSummary reports the outcome of a buffer reshape operation.
type ReshapeSummary struct {
	// Details holds per-commitment outcome descriptions for logging and audit.
	Details []string
	// Analyzed is the total number of commitments inspected.
	Analyzed int
	// Reshaped is the number of commitments exchanged or modified.
	Reshaped int
	// Skipped is the number of commitments that did not meet the utilization
	// threshold or were blocked by a spend cap.
	Skipped int
}
