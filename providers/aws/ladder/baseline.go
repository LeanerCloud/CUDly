package ladder

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// minBaselineSeriesDays is the shortest daily series AWSLadder will accept for
// baseline computation. Fewer days cannot produce a statistically meaningful
// low-water-mark; GetUsageBaseline returns an error when the series is shorter.
// The caller (engine) should configure LookbackDays >= minBaselineSeriesDays.
const minBaselineSeriesDays = 7

// maxMissingDays is the maximum number of calendar days that may be absent
// from the series within the configured lookback window and still yield a
// trustworthy baseline. CE data typically lags 24-48 hours, so a 30-day
// window may return 28-29 points; one extra day of tolerance is provided.
// A series with more than maxMissingDays absent days returns a coverage error.
// Distinct from minBaselineSeriesDays: this is a window-relative completeness
// check, not an absolute floor.
const maxMissingDays = 3

// maxSeriesAgeDays is the maximum age in calendar days of the most-recent data
// point in the on-demand series before the series is considered stale.
// CE cost data lags 24-48 hours, so yesterday's data (1 day old) is normal;
// a point older than maxSeriesAgeDays indicates the CE feed has stalled, the
// date-range parameter is wrong, or CE has temporarily stopped ingesting data
// for the account. GetUsageBaseline fails loud rather than computing a baseline
// from stale data that the utilization clamp would then trust.
const maxSeriesAgeDays = 3

// GetUsageBaseline computes a statistical low-water-mark from a daily
// on-demand-equivalent USD/hour series returned by the injected onDemandSeriesSource.
//
// Series semantics: each element is the average on-demand-equivalent USD/hour
// for one calendar day over the lookback window, ordered oldest-to-newest.
// The series is sourced from onDemandSeriesSource.GetOnDemandSeries, which is wired
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
//   - Series with too few points for the lookback window (more than
//     maxMissingDays gaps): hard error (coverage check).
//   - Most-recent point older than maxSeriesAgeDays days: hard error (stale
//     series; GetUsageBaseline fails loud rather than trusting stale data).
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

	points, err := a.onDemand.GetOnDemandSeries(ctx, a.cfg.Region, lookbackDays)
	if err != nil {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: on-demand series fetch failed: %w", err)
	}
	if len(points) == 0 {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: on-demand series is empty for region %s (series source returned no data)", a.cfg.Region)
	}
	if len(points) < minBaselineSeriesDays {
		return ladder.UsageBaseline{}, fmt.Errorf(
			"GetUsageBaseline: series length %d is below minimum %d days; extend the lookback window or check the on-demand series source",
			len(points), minBaselineSeriesDays,
		)
	}
	// Coverage check: the series must span most of the requested lookback
	// window. CE typically lags 24-48h, so up to maxMissingDays absent days
	// is acceptable; more indicates truncated date ranges or feed gaps.
	if minRequired := lookbackDays - maxMissingDays; len(points) < minRequired {
		return ladder.UsageBaseline{}, fmt.Errorf(
			"GetUsageBaseline: series has %d points for a %d-day lookback window (minimum %d = %d - maxMissingDays %d); the on-demand series source may have gaps or a truncated date range",
			len(points), lookbackDays, minRequired, lookbackDays, maxMissingDays,
		)
	}
	// Freshness check: the most-recent point must be within maxSeriesAgeDays.
	// A stale tail means the CE feed has stopped or the date range is wrong;
	// computing a baseline from stale data would yield a confidently wrong clamp.
	if fErr := validateSeriesFreshness(points); fErr != nil {
		return ladder.UsageBaseline{}, fmt.Errorf("GetUsageBaseline: %w", fErr)
	}

	series := extractValues(points)
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

// validateSeriesFreshness returns an error when the most-recent DailyPoint is
// older than maxSeriesAgeDays calendar days. It is called after the series has
// been confirmed non-empty and above minBaselineSeriesDays by GetUsageBaseline,
// so len(points) > 0 is guaranteed.
//
// Age is computed as integer days (floor of elapsed hours / 24), which is
// precise enough for a 3-day threshold and avoids DST and leap-second edge
// cases that a Date difference would introduce.
func validateSeriesFreshness(points []DailyPoint) error {
	latestDate := points[len(points)-1].Date
	ageDays := int(time.Since(latestDate).Hours() / 24)
	if ageDays > maxSeriesAgeDays {
		return fmt.Errorf(
			"on-demand series is stale: most recent data point is %s (%d days old, maximum %d); check that the CE data feed is active or that the lookback date range ends near today",
			latestDate.Format("2006-01-02"), ageDays, maxSeriesAgeDays,
		)
	}
	return nil
}

// extractValues returns the USDPerHour field of each DailyPoint as a plain
// []float64, ordered oldest-to-newest, for use by validateSeries and
// nearestRankPercentile. The Date fields are consumed by validateSeriesFreshness
// and not needed downstream.
func extractValues(points []DailyPoint) []float64 {
	out := make([]float64, len(points))
	for i, p := range points {
		out[i] = p.USDPerHour
	}
	return out
}

// validateSeries rejects series containing non-finite (NaN/Inf) or negative
// elements at the trust boundary: the series is injected via onDemandSeriesSource,
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
