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
//
// RDS gets special handling further down — same instance type often runs
// multiple engines in the same region, and CE GroupBy is limited to two
// dimensions, so we fan out across engines via the DATABASE_ENGINE filter
// instead of the GroupBy.
var coverageServiceFilters = []string{
	"Amazon Elastic Compute Cloud - Compute", // EC2
	"Amazon ElastiCache",                     // ElastiCache
	"Amazon OpenSearch Service",              // OpenSearch
	"Amazon Redshift",                        // Redshift
	"Amazon MemoryDB",                        // MemoryDB
}

// rdsCoverageEngines enumerates CE's DATABASE_ENGINE dimension values for
// RDS recommendations. CUDly's parser normalises engine names to lowercase
// shorthand (e.g. "aurora-postgresql") for rec.Details.Engine; CE expects
// the human-readable form here (e.g. "Aurora PostgreSQL"). The
// rdsEngineKeyFromCE / rdsEngineKeyFromRec helpers normalise both sides to
// the same lookup key.
var rdsCoverageEngines = []string{
	"MySQL",
	"PostgreSQL",
	"MariaDB",
	"Oracle",
	"SQL Server",
	"Aurora MySQL",
	"Aurora PostgreSQL",
}

const rdsServiceFilter = "Amazon Relational Database Service"

// PoolCoverageMap maps a pool key to the share of historical demand already
// covered by existing reservations in that pool, expressed as a 0-100
// percentage. Used by --target-coverage sizing to subtract existing
// commitments from the under-buy formula. Non-RDS keys are
// "region:instance_type"; RDS keys are "region:instance_type:engine"
// because the same instance type often runs different engines in the same
// region and their existing-RI coverage doesn't bleed across.
type PoolCoverageMap map[string]float64

// poolKey returns the canonical non-RDS lookup key for a
// (region, instance_type, account) tuple. All inputs are lower-cased so
// callers don't have to normalise case at every site. Account is the
// AWS-side 12-digit linked-account ID (kept as-is — it's already digits).
func poolKey(region, instanceType, account string) string {
	return strings.ToLower(region) + ":" + strings.ToLower(instanceType) + ":" + account
}

// rdsPoolKey returns the engine-aware lookup key for an RDS pool. CE's
// DATABASE_ENGINE dimension uses human-readable strings ("Aurora MySQL"),
// while CUDly's parser stores the shorthand on rec.Details.Engine
// ("aurora-mysql"). Both forms normalise to the same lookup key here so
// the producer (per-engine fetcher) and consumer (apply helper) agree.
// Account is the AWS-side linked-account ID, mirroring the LINKED_ACCOUNT
// dimension we group by in CE.
func rdsPoolKey(region, instanceType, engine, account string) string {
	return strings.ToLower(region) + ":" + strings.ToLower(instanceType) + ":" + normaliseRDSEngine(engine) + ":" + account
}

// normaliseRDSEngine canonicalises an engine string from either CE's
// "Aurora MySQL" form or the parser's "aurora-mysql" form to a single
// lowercase no-spaces representation ("auroramysql"). Strips spaces and
// hyphens so both producer and consumer of the coverage map collapse to
// the same key.
func normaliseRDSEngine(engine string) string {
	s := strings.ToLower(engine)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
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
func (c *Client) GetRICoverageMap(ctx context.Context, lookbackDays int, regions []string) (PoolCoverageMap, error) {
	if lookbackDays <= 0 {
		lookbackDays = 30
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	out := make(PoolCoverageMap)
	// CE's GroupBy is capped at two dimensions. We group by LINKED_ACCOUNT
	// + INSTANCE_TYPE and push REGION into the Filter so coverage comes
	// back per linked account in the region we're scanning. Without this
	// per-account split, CE's org-wide aggregate would average a heavily-
	// covered account's coverage into accounts with zero existing RIs in
	// the same pool — see #338 design discussion.
	//
	// Cost: one CE call per (service, region) for non-RDS; one CE call per
	// (engine, region) for RDS. For shift-prd's 5 services × ~23 regions +
	// 7 engines × 23 regions ≈ 270 calls (~$2.70/run). Empty regions
	// return quickly so the bound is loose in practice.
	for _, region := range regions {
		for _, service := range coverageServiceFilters {
			if err := c.fetchCoverageForServiceRegion(ctx, startStr, endStr, service, region, out); err != nil {
				return nil, fmt.Errorf("fetching coverage for service %q region %q: %w", service, region, err)
			}
		}
		for _, engine := range rdsCoverageEngines {
			if err := c.fetchCoverageForRDSEngineRegion(ctx, startStr, endStr, engine, region, out); err != nil {
				return nil, fmt.Errorf("fetching coverage for RDS engine %q region %q: %w", engine, region, err)
			}
		}
	}
	return out, nil
}

// fetchCoverageForRDSEngineRegion runs the paged GetReservationCoverage
// loop for one (RDS engine, region) tuple. Writes entries keyed by
// "region:instance_type:engine:account" so the apply helper looks up
// per-engine per-account coverage. This solves both the cross-engine
// bleed (db.r7g.large with heavy Aurora coverage looking covered for
// MySQL too) and the cross-account bleed (one account's full coverage
// dragging up the org-wide average against accounts with zero coverage).
func (c *Client) fetchCoverageForRDSEngineRegion(ctx context.Context, startStr, endStr, engine, region string, out PoolCoverageMap) error {
	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(startStr),
			End:   aws.String(endStr),
		},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("LINKED_ACCOUNT")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("INSTANCE_TYPE")},
		},
		Filter: &types.Expression{
			And: []types.Expression{
				{Dimensions: &types.DimensionValues{
					Key:    types.DimensionService,
					Values: []string{rdsServiceFilter},
				}},
				{Dimensions: &types.DimensionValues{
					Key:    types.DimensionDatabaseEngine,
					Values: []string{engine},
				}},
				{Dimensions: &types.DimensionValues{
					Key:    types.DimensionRegion,
					Values: []string{region},
				}},
			},
		},
		Metrics: []string{"Hour"},
	}

	var token *string
	for {
		input.NextPageToken = token
		result, err := c.fetchCoveragePage(ctx, input)
		if err != nil {
			return err
		}
		accumulateRDSEngineAccountGroups(out, result.CoveragesByTime, engine, region)
		if result.NextPageToken == nil || *result.NextPageToken == "" {
			return nil
		}
		token = result.NextPageToken
	}
}

// fetchCoverageForServiceRegion runs the paged GetReservationCoverage loop
// for one (non-RDS service, region) tuple. Writes entries keyed by
// "region:instance_type:account". Same per-account split as the RDS path,
// without the engine dimension (non-RDS services don't have it).
//
// "Hour" tells CE to include the Hour coverage block in the response;
// CoverageHoursPercentage inside that block is what we actually parse.
// HoursPercentage isn't a valid Metrics value (CE rejects it with
// ValidationException) — Metrics names the block, the percentage field
// is computed and included automatically.
func (c *Client) fetchCoverageForServiceRegion(ctx context.Context, startStr, endStr, service, region string, out PoolCoverageMap) error {
	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(startStr),
			End:   aws.String(endStr),
		},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("LINKED_ACCOUNT")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("INSTANCE_TYPE")},
		},
		Filter: &types.Expression{
			And: []types.Expression{
				{Dimensions: &types.DimensionValues{
					Key:    types.DimensionService,
					Values: []string{service},
				}},
				{Dimensions: &types.DimensionValues{
					Key:    types.DimensionRegion,
					Values: []string{region},
				}},
			},
		},
		Metrics: []string{"Hour"},
	}

	var token *string
	for {
		input.NextPageToken = token
		result, err := c.fetchCoveragePage(ctx, input)
		if err != nil {
			return err
		}
		accumulateAccountGroups(out, result.CoveragesByTime, region)
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

// accumulateAccountGroups walks a page of CE coverage responses grouped by
// LINKED_ACCOUNT + INSTANCE_TYPE and writes (region, instance_type, account)
// → HoursPercentage entries into out. The region arg comes from the caller
// (we filter by region; CE doesn't echo it back in the group attributes).
func accumulateAccountGroups(out PoolCoverageMap, byTime []types.CoverageByTime, region string) {
	for _, period := range byTime {
		for _, group := range period.Groups {
			account, instType := extractGroupAccountInstanceType(group.Attributes)
			if account == "" || instType == "" {
				continue
			}
			if group.Coverage == nil || group.Coverage.CoverageHours == nil ||
				group.Coverage.CoverageHours.CoverageHoursPercentage == nil {
				continue
			}
			pct := parseFloat(aws.ToString(group.Coverage.CoverageHours.CoverageHoursPercentage))
			out[poolKey(region, instType, account)] = pct
		}
	}
}

// accumulateRDSEngineAccountGroups is the RDS variant of accumulateAccountGroups
// that writes engine-aware keys. The engine and region args come from the
// caller (both are in the Filter, not the GroupBy).
func accumulateRDSEngineAccountGroups(out PoolCoverageMap, byTime []types.CoverageByTime, engine, region string) {
	for _, period := range byTime {
		for _, group := range period.Groups {
			account, instType := extractGroupAccountInstanceType(group.Attributes)
			if account == "" || instType == "" {
				continue
			}
			if group.Coverage == nil || group.Coverage.CoverageHours == nil ||
				group.Coverage.CoverageHours.CoverageHoursPercentage == nil {
				continue
			}
			pct := parseFloat(aws.ToString(group.Coverage.CoverageHours.CoverageHoursPercentage))
			out[rdsPoolKey(region, instType, engine, account)] = pct
		}
	}
}

// extractGroupAccountInstanceType reads the LINKED_ACCOUNT and INSTANCE_TYPE
// values from CE's Attributes map. CE sends keys in camelCase ("account" /
// "instanceType") even though the GroupBy input expects SCREAMING_SNAKE_CASE
// ("LINKED_ACCOUNT" / "INSTANCE_TYPE"); normalise both sides by stripping
// underscores and lower-casing before comparing. Returns ("", "") when
// either dimension is missing — caller skips those groups.
func extractGroupAccountInstanceType(attrs map[string]string) (account, instanceType string) {
	for k, v := range attrs {
		switch strings.ToLower(strings.ReplaceAll(k, "_", "")) {
		case "account", "linkedaccount":
			account = v // 12-digit AWS account ID, case-insensitive but already digits
		case "instancetype":
			instanceType = strings.ToLower(v)
		}
	}
	return account, instanceType
}

// ApplyCoverageMapToRecommendations sets ExistingCoveragePct on each rec
// whose pool key appears in the map. Pool key shape depends on service:
// RDS recs (DatabaseDetails carrying an engine) look up by
// "region:instance_type:engine"; other services look up by
// "region:instance_type". Recs without a match stay at zero, which the
// sizing path treats as "no signal" and falls back to the
// no-existing-commitments formula.
//
// Mutates recs in place to mirror the way the sizing pipeline already
// hands recs around by value within each loop iteration.
func ApplyCoverageMapToRecommendations(recs []common.Recommendation, coverage PoolCoverageMap) {
	if len(coverage) == 0 {
		return
	}
	for i := range recs {
		key := lookupPoolKey(recs[i])
		if pct, ok := coverage[key]; ok {
			recs[i].ExistingCoveragePct = pct
		}
	}
}

// lookupPoolKey returns the pool key for a recommendation. RDS uses the
// engine-aware form; non-RDS uses the simpler form. Both include the
// linked-account ID so the per-account fetcher's keys match — without
// this, CE's org-wide aggregate would bleed across accounts.
func lookupPoolKey(rec common.Recommendation) string {
	if engine := rdsEngineFromRec(rec); engine != "" {
		return rdsPoolKey(rec.Region, rec.ResourceType, engine, rec.Account)
	}
	return poolKey(rec.Region, rec.ResourceType, rec.Account)
}

// rdsEngineFromRec extracts the RDS engine string from a recommendation's
// polymorphic Details, returning "" when the rec isn't an RDS rec or the
// engine isn't populated. Handles both pointer and value forms of
// DatabaseDetails because the live parser uses pointers and the CSV
// loader uses values.
func rdsEngineFromRec(rec common.Recommendation) string {
	switch details := rec.Details.(type) {
	case *common.DatabaseDetails:
		if details != nil {
			return details.Engine
		}
	case common.DatabaseDetails:
		return details.Engine
	}
	return ""
}
