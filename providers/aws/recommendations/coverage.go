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

// coverageServiceFilters lists the Cost Explorer service-dimension values
// we issue GetReservationCoverage against. CE returns only EC2 coverage when
// no SERVICE filter is set, so any account that runs RDS/ElastiCache/
// OpenSearch/Redshift RIs needs a per-service call. The service strings here
// match CE's canonical dimension values (case-sensitive); the comments
// indicate which CUDly internal common.ServiceType they correspond to.
var coverageServiceFilters = []string{
	"Amazon Elastic Compute Cloud - Compute", // EC2
	"Amazon Relational Database Service",     // RDS (incl. Aurora)
	"Amazon ElastiCache",                     // ElastiCache
	"Amazon OpenSearch Service",              // OpenSearch
	"Amazon Redshift",                        // Redshift
	"Amazon MemoryDB",                        // MemoryDB
}

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
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	out := make(PoolCoverageMap)
	for _, service := range coverageServiceFilters {
		if err := c.fetchCoverageForService(ctx, startStr, endStr, service, out); err != nil {
			return nil, fmt.Errorf("fetching coverage for service %q: %w", service, err)
		}
	}
	return out, nil
}

// fetchCoverageForService runs the paged GetReservationCoverage loop for a
// single SERVICE dimension value and writes the (region, instance_type) →
// HoursPercentage entries into out. Split per-service because CE returns
// only EC2 coverage when no SERVICE filter is set, so a single
// no-filter call hides RDS/ElastiCache/etc. coverage entirely.
func (c *Client) fetchCoverageForService(ctx context.Context, startStr, endStr, service string, out PoolCoverageMap) error {
	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(startStr),
			End:   aws.String(endStr),
		},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("REGION")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("INSTANCE_TYPE")},
		},
		Filter: &types.Expression{
			Dimensions: &types.DimensionValues{
				Key:    types.DimensionService,
				Values: []string{service},
			},
		},
		// "Hour" tells CE to include the Hour coverage block in the response;
		// CoverageHoursPercentage inside that block is what we actually parse.
		// HoursPercentage isn't a valid Metrics value (CE rejects it with
		// ValidationException) — Metrics names the block, the percentage
		// field is computed and included automatically.
		Metrics: []string{"Hour"},
	}

	var token *string
	for {
		input.NextPageToken = token
		result, err := c.fetchCoveragePage(ctx, input)
		if err != nil {
			return err
		}
		accumulateCoverageGroups(out, result.CoveragesByTime)
		if result.NextPageToken == nil || *result.NextPageToken == "" {
			return nil
		}
		token = result.NextPageToken
	}
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
			region, instType := extractGroupPoolKey(group.Attributes)
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

// extractGroupPoolKey reads the REGION and INSTANCE_TYPE values from CE's
// Attributes map. CE sends keys in camelCase (e.g. "region", "instanceType")
// even though the GroupBy input expects SCREAMING_SNAKE_CASE
// ("REGION", "INSTANCE_TYPE"); normalise both sides by stripping underscores
// and lower-casing before comparing. Returns ("", "") when either dimension
// is missing — caller skips those groups.
func extractGroupPoolKey(attrs map[string]string) (region, instanceType string) {
	for k, v := range attrs {
		switch strings.ToLower(strings.ReplaceAll(k, "_", "")) {
		case "region":
			region = strings.ToLower(v)
		case "instancetype":
			instanceType = strings.ToLower(v)
		}
	}
	return region, instanceType
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
