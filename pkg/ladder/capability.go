package ladder

import (
	"context"

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
	//
	// Role-cardinality contract (enforced by the engine): the returned set
	// must contain exactly one layer carrying RoleFlex, at most one carrying
	// RoleBase, and at most one carrying RoleBuffer. The only permitted
	// multi-role merge is base+buffer on a single layer (e.g. an Azure
	// reservation serving both roles).
	SupportedLayers() []LayerSpec

	// ListCommitments returns all active commitments for the given scope.
	ListCommitments(ctx context.Context, scope Scope) ([]common.Commitment, error)

	// GetLayerStates returns a point-in-time snapshot for each supported layer.
	// Layers with no active commitments carry explicit zeros in
	// ExistingUSDPerHour/ExpiringUSDPerHour; nil is reserved for genuinely
	// unmeasured metrics (e.g. UtilizationPct on an empty layer) and is
	// treated as missing data by the engine.
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
//
// The money caps are config-boundary floats, consistent with
// LadderConfig.MaxHourlyCommitPerRun (implementations convert to
// pkg/exchange float64 anyway). Convert via ratFromFloat where exact math
// is needed; NaN/Inf/non-positive values are rejected wherever these caps
// are validated.
type BufferReshapeConfig struct {
	// MaxPaymentPerExchangeUSD caps the payment for a single exchange. nil
	// means no per-exchange cap is applied.
	MaxPaymentPerExchangeUSD *float64
	// MaxPaymentDailyUSD caps the total exchange payments for the current day.
	// nil means no daily cap is applied.
	MaxPaymentDailyUSD *float64
	// UtilizationThresholdPct triggers a reshape when commitment utilization
	// drops below this percentage.
	UtilizationThresholdPct float64
	// LookbackDays is the window used to measure utilization when deciding
	// whether to trigger a reshape.
	LookbackDays int
	// DryRun, when true, simulates the reshape without executing any exchanges.
	DryRun bool
	// LadderRunID links the exchange records this reshape creates to the ladder
	// run that triggered it, so the exchange layer scopes its pending
	// cancellation to this origin (ladder) instead of the standalone task's
	// pendings (gap G10 / issue #1348). Nil means "no ladder run" (the caller is
	// not a ladder run); the reshape runner then behaves as the standalone
	// origin. The concrete runner (wired in L16) MUST forward this to
	// exchange.RunAutoExchangeParams.LadderRunID — the exchangeRunner seam
	// requires it so a future runner cannot silently drop it and reintroduce the
	// cross-origin cancellation bug.
	LadderRunID *string
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
