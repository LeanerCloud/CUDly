package ladder

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// minBaselineSeriesDays is the shortest daily series AWSLadder will accept for
// baseline computation. Fewer days cannot produce a statistically meaningful
// low-water-mark; GetUsageBaseline returns an error when the series is shorter.
// The caller (engine) should configure LookbackDays >= minBaselineSeriesDays.
const minBaselineSeriesDays = 7

// GetUsageBaseline computes a statistical low-water-mark from a daily
// on-demand-equivalent USD/hour series returned by the injected coverageSource.
//
// Series semantics: each element is the average on-demand-equivalent USD/hour
// for one calendar day over the lookback window, ordered oldest-to-newest.
// The series is sourced from coverageSource.GetOnDemandSeries, which is wired
// in a later PR to call CE GetCostAndUsage (Granularity=Daily, on-demand
// usage-type filter). Until that wiring lands, callers receive a data-source
// error from GetOnDemandSeries.
//
// Limitations:
//   - The series covers only the on-demand costs reported by the coverage
//     source. Until the CE GetCostAndUsage wiring lands, this will be an error.
//   - The series is EC2-scoped (the initial coverage source implementation
//     covers EC2 on-demand cost only, not RDS/ElastiCache/etc.).
//   - LowWaterUSDPerHour is the nearest-rank percentile of the daily series
//     (see nearestRankPercentile).
//   - StableUSDPerHour is nil: per the pkg/ladder contract (types.go), Stable
//     is the post-buffer-fraction estimate — a producer obligation this
//     implementation cannot yet meet (no stable-usage estimator exists for
//     AWS). Returning nil triggers the engine's documented degradation
//     ("stable usage unknown; routing all core gap to flex"), which is honest
//     and conservative. Do NOT alias it to LowWater: the engine consumes
//     Stable verbatim as the base-layer cap and would over-commit the base.
//
// Error conditions:
//   - Series empty: hard error (no data from the coverage source).
//   - Series shorter than minBaselineSeriesDays: hard error (insufficient
//     history for a reliable percentile estimate).
//   - Series containing a non-finite (NaN/Inf) or negative element: hard error
//     naming the offending index (a cost series must be finite and >= 0).
//   - percentile not in (0, 100]: hard error (out-of-range).
func (a *AWSLadder) GetUsageBaseline(ctx context.Context, scope ladder.Scope, lookbackDays int, percentile float64) (ladder.UsageBaseline, error) {
	if err := a.validateScope(scope); err != nil {
		return ladder.UsageBaseline{}, err
	}
	if err := validateBaselineArgs(lookbackDays, percentile); err != nil {
		return ladder.UsageBaseline{}, err
	}

	series, err := a.coverage.GetOnDemandSeries(ctx, a.cfg.Region, lookbackDays)
	if err != nil {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: on-demand series fetch failed: %w", err)
	}
	if len(series) == 0 {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: on-demand series is empty for region %s (coverage source returned no data)", a.cfg.Region)
	}
	if len(series) < minBaselineSeriesDays {
		return ladder.UsageBaseline{}, fmt.Errorf(
			"GetUsageBaseline: series length %d is below minimum %d days; extend the lookback window or check the coverage source",
			len(series), minBaselineSeriesDays,
		)
	}
	if vErr := validateSeries(series); vErr != nil {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: %w", vErr)
	}

	lowWater, err := nearestRankPercentile(series, percentile)
	if err != nil {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: percentile computation failed: %w", err)
	}

	// StableUSDPerHour is intentionally nil: the pkg/ladder contract defines
	// Stable as the post-buffer-fraction estimate (a producer obligation), and
	// no stable-usage estimator exists for AWS yet. nil triggers the engine's
	// documented "stable usage unknown; routing all core gap to flex"
	// degradation — honest and conservative until a real estimator lands.
	return ladder.UsageBaseline{
		LowWaterUSDPerHour: ptr(lowWater),
		StableUSDPerHour:   nil,
		Series:             series,
		LookbackDays:       lookbackDays,
		Percentile:         percentile,
	}, nil
}

// validateSeries rejects series containing non-finite (NaN/Inf) or negative
// elements at the trust boundary: the series is injected via coverageSource,
// and a single bad element would silently corrupt the percentile (NaN makes
// the sort order undefined; a negative cost is impossible for on-demand spend).
// The error names the offending index so the data-source bug is traceable.
func validateSeries(series []float64) error {
	for i, v := range series {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("series element at index %d is not finite (%g); the on-demand series must contain only finite values", i, v)
		}
		if v < 0 {
			return fmt.Errorf("series element at index %d is negative (%g); on-demand cost values must be >= 0", i, v)
		}
	}
	return nil
}

// validateBaselineArgs checks lookbackDays and percentile for out-of-range
// values. Extracted to keep GetUsageBaseline under the cyclomatic limit.
func validateBaselineArgs(lookbackDays int, percentile float64) error {
	if lookbackDays <= 0 {
		return fmt.Errorf("GetUsageBaseline: lookbackDays %d must be > 0", lookbackDays)
	}
	if math.IsNaN(percentile) || !(percentile > 0 && percentile <= 100) {
		return fmt.Errorf("GetUsageBaseline: percentile %g must be in (0, 100]", percentile)
	}
	return nil
}

// nearestRankPercentile returns the p-th percentile of values using the
// nearest-rank method (NIST definition):
//
//	rank = ceil(p/100 * N)   (1-indexed, clamped to [1, N])
//	result = sorted_values[rank-1]
//
// Properties:
//   - Exact: no interpolation. The result is always a member of the input set.
//   - NaN-hostile: if any value in data is NaN, the sort is undefined and
//     the result is meaningless. GetUsageBaseline enforces finiteness via
//     validateSeries before calling this function; other callers must do the
//     same.
//   - Empty slice: returns an error (caller must guard before calling).
//   - p==100: returns the maximum element (rank=N).
//   - p close to 0: rank rounds up to 1, returning the minimum element.
//
// No external statistics package is imported; this keeps the ladder package
// dependency-free for test purposes.
func nearestRankPercentile(data []float64, p float64) (float64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("nearestRankPercentile: empty data slice")
	}
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	n := float64(len(sorted))
	rank := int(math.Ceil(p / 100.0 * n))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1], nil
}
