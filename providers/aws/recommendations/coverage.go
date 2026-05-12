package recommendations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// PoolCoverageMap maps "region:instance_type" (lowercase) to the share of
// historical demand already covered by existing reservations in that pool,
// expressed as a 0-100 percentage. Used by --target-coverage sizing to
// subtract existing commitments from the under-buy formula.
type PoolCoverageMap map[string]float64

// poolKey returns the canonical lookup key for a (region, instance_type)
// pair. Both inputs are lower-cased so callers don't have to normalise
// case at every site.
func poolKey(region, instanceType string) string {
	return strings.ToLower(region) + ":" + strings.ToLower(instanceType)
}

// GetRICoverageMap fetches existing-RI coverage % over the last lookbackDays
// days, returning a map keyed by "region:instance_type". Operators wiring
// the result back onto Recommendation.ExistingCoveragePct should call
// ApplyCoverageMapToRecommendations rather than walking the map manually.
//
// CE's GetReservationCoverage accepts at most 2 GroupBy dimensions, so we
// pick REGION + INSTANCE_TYPE as the dominant per-pool dimensions across
// EC2, RDS, ElastiCache and OpenSearch RIs. Finer dimensions (OS, tenancy
// for EC2; engine, multi-AZ for RDS) are aggregated together — imprecise
// for mixed pools but the API doesn't let us slice finer in one call.
//
// Missing pools (no existing commitment in the pool) are omitted from the
// map; ApplyCoverageMapToRecommendations leaves ExistingCoveragePct at zero
// for those recs, which the sizing path treats as "no signal" and falls
// back to the no-existing-commitments formula.
func (c *Client) GetRICoverageMap(ctx context.Context, lookbackDays int) (PoolCoverageMap, error) {
	if lookbackDays <= 0 {
		lookbackDays = 30
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)

	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("REGION")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("INSTANCE_TYPE")},
		},
		Metrics: []string{"HoursPercentage"},
	}

	out := make(PoolCoverageMap)
	var token *string
	for {
		input.NextPageToken = token
		result, err := c.fetchCoveragePage(ctx, input)
		if err != nil {
			return nil, err
		}
		accumulateCoverageGroups(out, result.CoveragesByTime)
		if result.NextPageToken == nil || *result.NextPageToken == "" {
			break
		}
		token = result.NextPageToken
	}
	return out, nil
}

// fetchCoveragePage calls the Cost Explorer API with rate-limit retry.
// Mirrors fetchUtilizationPage so the two paths fail and back off the
// same way.
func (c *Client) fetchCoveragePage(ctx context.Context, input *costexplorer.GetReservationCoverageInput) (*costexplorer.GetReservationCoverageOutput, error) {
	c.rateLimiter.Reset()
	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}
		result, err := c.costExplorerClient.GetReservationCoverage(ctx, input)
		if !c.rateLimiter.ShouldRetry(err) {
			if err != nil {
				return nil, fmt.Errorf("failed to get reservation coverage: %w", err)
			}
			return result, nil
		}
	}
}

// accumulateCoverageGroups walks a page of CE coverage responses and writes
// the (region, instance_type) → HoursPercentage entries into out. Later
// pages overwrite earlier ones for the same key — CE returns one group per
// (region, instance_type) per time period, so collisions only happen across
// time periods and we keep the latest value (sufficient as a single-figure
// summary; finer time-series analysis is out of scope here).
func accumulateCoverageGroups(out PoolCoverageMap, byTime []types.CoverageByTime) {
	for _, period := range byTime {
		for _, group := range period.Groups {
			region := strings.ToLower(group.Attributes["REGION"])
			instType := strings.ToLower(group.Attributes["INSTANCE_TYPE"])
			if region == "" || instType == "" {
				continue
			}
			if group.Coverage == nil || group.Coverage.CoverageHours == nil ||
				group.Coverage.CoverageHours.CoverageHoursPercentage == nil {
				continue
			}
			pct := parseFloat(aws.ToString(group.Coverage.CoverageHours.CoverageHoursPercentage))
			out[poolKey(region, instType)] = pct
		}
	}
}

// ApplyCoverageMapToRecommendations sets ExistingCoveragePct on each rec
// whose (region, ResourceType) appears in the map. Recs without a match
// stay at zero, which the sizing path treats as "no signal" and falls
// back to the no-existing-commitments formula.
//
// Mutates recs in place to mirror the way the sizing pipeline already
// hands recs around by value within each loop iteration.
func ApplyCoverageMapToRecommendations(recs []common.Recommendation, coverage PoolCoverageMap) {
	if len(coverage) == 0 {
		return
	}
	for i := range recs {
		key := poolKey(recs[i].Region, recs[i].ResourceType)
		if pct, ok := coverage[key]; ok {
			recs[i].ExistingCoveragePct = pct
		}
	}
}
