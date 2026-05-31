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
// match CE's canonical dimension values (case-sensitive).
//
// RDS gets special handling — same instance type often runs multiple engines
// in the same region, and the AWS console coverage report further splits each
// engine row by deployment option (Single-AZ vs Multi-AZ), so we fan out
// across engines via the DATABASE_ENGINE filter and pull DEPLOYMENT_OPTION
// into the GroupBy alongside INSTANCE_TYPE.
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
// the human-readable form here (e.g. "Aurora PostgreSQL"). normaliseRDSEngine
// canonicalises both sides to the same lookup key.
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

// PoolCoverage captures the two CE coverage signals we use downstream: the
// share of historical demand already covered by existing reservations
// (Pct, 0-100) and the average concurrent-instances figure derived from
// TotalRunningHours over the lookback window (AvgInstancesPerHour).
//
// AvgInstancesPerHour matches the "Total running hours / window hours"
// figure shown in the AWS console reservations-coverage report; it's what
// --target-coverage sizing now anchors on (linear in avg × gap%) so a
// pool's CUDly buy lines up with the same math operators see in the
// console CSV. Zero means CE returned no running hours for the pool over
// the window — sizing then falls back to rec.AverageInstancesUsedPerHour
// from the rec parser (per-account signal from
// GetReservationPurchaseRecommendation).
type PoolCoverage struct {
	Pct                 float64
	AvgInstancesPerHour float64
}

// PoolCoverageMap maps a pool key to the (pct, avg) pair for that pool.
// Used by --target-coverage sizing to subtract existing commitments from
// the under-buy formula and to size linearly off the org-wide demand.
// Keys are org-wide (no account dimension) to match the AWS console's
// reservations-coverage report: non-RDS keys are "region:instance_type";
// RDS keys are "region:instance_type:engine:deployment" so Single-AZ vs
// Multi-AZ pools stay distinct (a Single-AZ RI cannot cover a Multi-AZ
// instance).
type PoolCoverageMap map[string]PoolCoverage

// poolKey returns the canonical non-RDS lookup key for a (region,
// instance_type) tuple. Both inputs are lower-cased so callers don't have
// to normalise case at every site.
func poolKey(region, instanceType string) string {
	return strings.ToLower(region) + ":" + strings.ToLower(instanceType)
}

// rdsPoolKey returns the engine + deployment-aware lookup key for an RDS
// pool. CE's DATABASE_ENGINE dimension uses human-readable strings
// ("Aurora MySQL"), while CUDly's parser stores the shorthand on
// rec.Details.Engine ("aurora-mysql"). DEPLOYMENT_OPTION ("Single-AZ" /
// "Multi-AZ") similarly maps to the parser's AZConfig ("single-az" /
// "multi-az"). Both sides normalise to the same lookup key here so the
// producer (per-engine fetcher) and consumer (apply helper) agree.
func rdsPoolKey(region, instanceType, engine, deployment string) string {
	return strings.ToLower(region) + ":" +
		strings.ToLower(instanceType) + ":" +
		normaliseRDSEngine(engine) + ":" +
		normaliseDeployment(deployment)
}

// normaliseRDSEngine canonicalises an engine string from either CE's
// display form ("Aurora MySQL", "PostgreSQL", "SQL Server") or the
// shorter forms returned by RDS APIs ("aurora-mysql", "postgres",
// "sqlserver-ee", "oracle-se2") to a single lowercase no-spaces
// representation. Strips spaces / hyphens / underscores AND collapses
// well-known short-form aliases plus edition suffixes so both producer
// (per-engine CE fetcher seeded from rdsCoverageEngines) and consumer
// (apply helper reading rec.Details.Engine) land on the same key
// regardless of which API surfaced the engine string.
//
// Specifically:
//   - "postgres" collapses to "postgresql" (the bare RDS engine slug
//     vs the CE display form).
//   - "sqlserver-ee" / "sqlserver-se" / "sqlserver-ex" / "sqlserver-web"
//     all collapse to "sqlserver" (CE returns the bare display form
//     "SQL Server" while RDS Describe* APIs return edition-suffixed
//     slugs).
//   - "oracle-ee" / "oracle-se" / "oracle-se1" / "oracle-se2" collapse
//     to "oracle".
//
// Without the alias collapse, a rec whose parser saw "postgres" (or an
// Oracle/SQL-Server edition slug) would miss the coverage entry the
// per-engine fetcher wrote under the display-form key, silently
// dropping ExistingCoveragePct to zero and over-buying for that pool.
func normaliseRDSEngine(engine string) string {
	s := strings.ToLower(engine)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	if s == "postgres" {
		return "postgresql"
	}
	for _, family := range []string{"sqlserver", "oracle"} {
		if strings.HasPrefix(s, family) {
			return family
		}
	}
	return s
}

// normaliseDeployment canonicalises a deployment-option string from either
// CE's "Single-AZ" / "Multi-AZ" form or the parser's "single-az" /
// "multi-az" form to a single lowercase no-spaces representation. The
// "Multi-AZ (readable standbys)" variant collapses to a distinct value
// ("multiazreadablestandbys") since it's a distinct RI scope.
//
// An empty input yields "" (not "unknown") so missing-deployment recs
// produce a deterministic miss in the lookup map rather than colliding
// with a real bucket.
func normaliseDeployment(deployment string) string {
	s := strings.ToLower(deployment)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	return s
}

// GetRICoverageMap fetches existing-RI coverage % and avg-running-instances
// over the last lookbackDays days, returning a map keyed by org-wide pool
// (no account dimension — matches the AWS console reservations-coverage
// report). Operators wiring the result back onto Recommendations should
// call ApplyCoverageMapToRecommendations rather than walking the map
// manually.
//
// Non-RDS calls group by INSTANCE_TYPE alone (REGION + SERVICE in the
// Filter). RDS calls group by INSTANCE_TYPE + DEPLOYMENT_OPTION (REGION +
// SERVICE + DATABASE_ENGINE in the Filter) so the resulting keys split
// Single-AZ vs Multi-AZ pools, which have separate RI scopes.
//
// Missing pools (no demand in the pool over the window) are omitted from
// the map; ApplyCoverageMapToRecommendations leaves
// rec.ExistingCoveragePct at zero for those recs, which the sizing path
// treats as "no signal" and falls back to the no-existing-commitments
// formula.
func (c *Client) GetRICoverageMap(ctx context.Context, lookbackDays int, regions []string) (PoolCoverageMap, error) {
	if lookbackDays <= 0 {
		lookbackDays = 30
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")
	windowHours := float64(lookbackDays * 24)

	out := make(PoolCoverageMap)
	// Cost: one CE call per (service, region) for non-RDS; one CE call per
	// (engine, region) for RDS. For a 5-service × 23-region survey + 7
	// engines × 23 regions ≈ 270 calls (~$2.70/run). Empty regions return
	// quickly so the bound is loose in practice.
	for _, region := range regions {
		for _, service := range coverageServiceFilters {
			if err := c.fetchCoverageForServiceRegion(ctx, startStr, endStr, windowHours, service, region, out); err != nil {
				return nil, fmt.Errorf("fetching coverage for service %q region %q: %w", service, region, err)
			}
		}
		for _, engine := range rdsCoverageEngines {
			if err := c.fetchCoverageForRDSEngineRegion(ctx, startStr, endStr, windowHours, engine, region, out); err != nil {
				return nil, fmt.Errorf("fetching coverage for RDS engine %q region %q: %w", engine, region, err)
			}
		}
	}
	return out, nil
}

// fetchCoverageForRDSEngineRegion fetches coverage for one (RDS engine,
// region) tuple. Writes entries keyed by
// "region:instance_type:engine:deployment" so the apply helper looks up
// per-engine per-deployment coverage. This solves both the cross-engine
// bleed (db.r7g.large with heavy Aurora coverage looking covered for
// MySQL too) and the cross-deployment bleed (a Single-AZ RI being
// credited against Multi-AZ demand).
func (c *Client) fetchCoverageForRDSEngineRegion(ctx context.Context, startStr, endStr string, windowHours float64, engine, region string, out PoolCoverageMap) error {
	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{Start: aws.String(startStr), End: aws.String(endStr)},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String(string(types.DimensionInstanceType))},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String(string(types.DimensionDeploymentOption))},
		},
		Filter:  rdsEngineRegionFilter(engine, region),
		Metrics: []string{"Hour"},
	}
	return c.fetchCoveragePaged(ctx, input, func(instType, deployment string, cov PoolCoverage) {
		out[rdsPoolKey(region, instType, engine, deployment)] = cov
	}, windowHours)
}

// fetchCoverageForServiceRegion fetches coverage for one (non-RDS
// service, region) tuple. Writes entries keyed by "region:instance_type"
// — non-RDS services don't have an engine or deployment split worth
// tracking the way RDS does, so we group by INSTANCE_TYPE alone.
//
// "Hour" tells CE to include the Hour coverage block in the response;
// CoverageHoursPercentage + TotalRunningHours inside that block are what
// we actually parse. HoursPercentage isn't a valid Metrics value (CE
// rejects it with ValidationException) — Metrics names the block, the
// percentage / hours fields are computed and included automatically.
func (c *Client) fetchCoverageForServiceRegion(ctx context.Context, startStr, endStr string, windowHours float64, service, region string, out PoolCoverageMap) error {
	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{Start: aws.String(startStr), End: aws.String(endStr)},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String(string(types.DimensionInstanceType))},
		},
		Filter:  serviceRegionFilter(service, region),
		Metrics: []string{"Hour"},
	}
	return c.fetchCoveragePaged(ctx, input, func(instType, _ string, cov PoolCoverage) {
		out[poolKey(region, instType)] = cov
	}, windowHours)
}

// fetchCoveragePaged runs the paginated GetReservationCoverage loop and
// invokes record on each group with a non-empty INSTANCE_TYPE and a
// valid Coverage block. The keyed-write logic is callsite-specific
// (RDS keys carry engine + deployment, non-RDS keys don't), so record
// closes over the key shape the caller wants.
func (c *Client) fetchCoveragePaged(
	ctx context.Context,
	input *costexplorer.GetReservationCoverageInput,
	record func(instType, deployment string, cov PoolCoverage),
	windowHours float64,
) error {
	var token *string
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("coverage: pagination cancelled: %w", err)
		}
		input.NextPageToken = token
		result, err := c.fetchCoveragePage(ctx, input)
		if err != nil {
			return err
		}
		for _, period := range result.CoveragesByTime {
			for _, group := range period.Groups {
				instType, deployment := extractGroupAttributes(group.Attributes)
				if instType == "" {
					continue
				}
				cov, ok := poolCoverageFromGroup(group, windowHours)
				if !ok {
					continue
				}
				record(instType, deployment, cov)
			}
		}
		if result.NextPageToken == nil || *result.NextPageToken == "" {
			return nil
		}
		token = result.NextPageToken
	}
}

// rdsEngineRegionFilter builds the CE Filter expression scoping a
// GetReservationCoverage call to a single (RDS engine, region) tuple.
// Extracted so the fetch path doesn't bury the filter shape inside the
// input struct literal.
func rdsEngineRegionFilter(engine, region string) *types.Expression {
	return &types.Expression{
		And: []types.Expression{
			{Dimensions: &types.DimensionValues{Key: types.DimensionService, Values: []string{rdsServiceFilter}}},
			{Dimensions: &types.DimensionValues{Key: types.DimensionDatabaseEngine, Values: []string{engine}}},
			{Dimensions: &types.DimensionValues{Key: types.DimensionRegion, Values: []string{region}}},
		},
	}
}

// serviceRegionFilter builds the CE Filter expression scoping a
// GetReservationCoverage call to a single (service, region) tuple
// (non-RDS variant: no DATABASE_ENGINE dimension).
func serviceRegionFilter(service, region string) *types.Expression {
	return &types.Expression{
		And: []types.Expression{
			{Dimensions: &types.DimensionValues{Key: types.DimensionService, Values: []string{service}}},
			{Dimensions: &types.DimensionValues{Key: types.DimensionRegion, Values: []string{region}}},
		},
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

// poolCoverageFromGroup pulls the (pct, avg) pair from a CE response
// group's CoverageHours block. Returns (zero-value, false) when the block
// is absent or missing the percentage field — caller drops the group.
// AvgInstancesPerHour = TotalRunningHours / windowHours, matching the AWS
// console's "instances over the lookback window" view. windowHours is
// 24 * lookbackDays from the GetRICoverageMap caller.
func poolCoverageFromGroup(group types.ReservationCoverageGroup, windowHours float64) (PoolCoverage, bool) {
	if group.Coverage == nil || group.Coverage.CoverageHours == nil ||
		group.Coverage.CoverageHours.CoverageHoursPercentage == nil {
		return PoolCoverage{}, false
	}
	pct := parseFloat(aws.ToString(group.Coverage.CoverageHours.CoverageHoursPercentage))
	var avg float64
	if windowHours > 0 && group.Coverage.CoverageHours.TotalRunningHours != nil {
		avg = parseFloat(aws.ToString(group.Coverage.CoverageHours.TotalRunningHours)) / windowHours
	}
	return PoolCoverage{Pct: pct, AvgInstancesPerHour: avg}, true
}

// extractGroupAttributes reads INSTANCE_TYPE and DEPLOYMENT_OPTION values
// from CE's Attributes map. CE sends keys in camelCase ("instanceType",
// "deploymentOption") even though the GroupBy input expects
// SCREAMING_SNAKE_CASE ("INSTANCE_TYPE", "DEPLOYMENT_OPTION"); normalise
// both sides by stripping underscores and lower-casing before comparing.
// Returns empty strings for absent dimensions — the deployment slot is
// optional (non-RDS callers don't group by it and won't pass it through).
func extractGroupAttributes(attrs map[string]string) (instanceType, deployment string) {
	for k, v := range attrs {
		switch strings.ToLower(strings.ReplaceAll(k, "_", "")) {
		case "instancetype":
			instanceType = strings.ToLower(v)
		case "deploymentoption":
			deployment = v
		}
	}
	return instanceType, deployment
}

// ApplyCoverageMapToRecommendations sets ExistingCoveragePct on each rec
// whose pool key appears in the map, and rebalances each rec's
// AverageInstancesUsedPerHour so that the per-pool sum across recs equals
// the coverage map's org-wide AvgInstancesPerHour for the pool. Pool key
// shape depends on service: RDS recs (DatabaseDetails carrying an engine
// + AZConfig) look up by "region:instance_type:engine:deployment"; other
// services look up by "region:instance_type". Recs without a match stay
// at zero, which the sizing path treats as "no signal" and falls back to
// the no-existing-commitments formula.
//
// Why rebalance instead of just trusting per-rec avgs: AWS's
// GetReservationPurchaseRecommendation returns one rec per (pool, account)
// where it sees demand worth recommending. When AWS rec API only returns
// per-account avgs that sum to less than what CE coverage reports
// org-wide for the same pool (some linked accounts in the pool aren't
// surfaced as recs), per-rec sizing under-buys for the pool as a whole.
// Rebalancing scales each rec's avg by (cov.avg / sum-of-rec-avgs-in-pool)
// so the sized per-rec purchases sum to what the coverage CSV's gap math
// implies. When AWS rec API already matches coverage, the scale factor is
// ~1.0 and behaviour is unchanged. When multiple recs have zero avg
// (no per-account signal at all), the coverage avg is split evenly across
// them so the total still lines up.
//
// Mutates recs in place to mirror the way the sizing pipeline already
// hands recs around by value within each loop iteration.
func ApplyCoverageMapToRecommendations(recs []common.Recommendation, coverage PoolCoverageMap) {
	if len(coverage) == 0 {
		return
	}
	// First pass: index recs by pool key and sum per-account avgs so we
	// can compute the scaling factor needed to land the per-pool total on
	// the coverage's org-wide avg.
	poolIdx := make(map[string][]int)
	poolAvgSum := make(map[string]float64)
	for i := range recs {
		key := lookupPoolKey(recs[i])
		poolIdx[key] = append(poolIdx[key], i)
		if recs[i].AverageInstancesUsedPerHour > 0 {
			poolAvgSum[key] += recs[i].AverageInstancesUsedPerHour
		}
	}
	// Second pass: set ExistingCoveragePct + scale AverageInstancesUsedPerHour
	// per rec so the pool's recs sum to cov.AvgInstancesPerHour.
	for i := range recs {
		key := lookupPoolKey(recs[i])
		cov, ok := coverage[key]
		if !ok {
			continue
		}
		recs[i].ExistingCoveragePct = cov.Pct
		recs[i].ExistingCoverageKnown = true
		if cov.AvgInstancesPerHour <= 0 {
			// No org-wide avg signal — leave rec.avg as-is (rec API's
			// per-account number is the only signal we have for sizing).
			continue
		}
		if recSum := poolAvgSum[key]; recSum > 0 {
			// Scale this rec's per-account avg by its proportional share
			// of the org-wide avg. When AWS rec API's per-account total
			// matches CE coverage, scale ≈ 1 (no-op). When AWS rec API
			// under-counts (some accounts not surfaced), scale > 1 and
			// each rec's sized buy grows proportionally so the per-pool
			// sum lands on the coverage figure.
			recs[i].AverageInstancesUsedPerHour *= cov.AvgInstancesPerHour / recSum
		} else {
			// Every rec in this pool came back with zero avg — split the
			// coverage's avg evenly across them. Equal distribution is
			// the only fair choice when there's no per-account signal to
			// weight by.
			recs[i].AverageInstancesUsedPerHour = cov.AvgInstancesPerHour / float64(len(poolIdx[key]))
		}
	}
}

// lookupPoolKey returns the pool key for a recommendation. RDS uses the
// engine + deployment-aware form; non-RDS uses the region+type form. Keys
// are org-wide (no account dimension) so the same pool seen from any
// linked account looks up the same coverage entry — matches the AWS
// console reservations-coverage report.
func lookupPoolKey(rec common.Recommendation) string {
	if engine, deployment := rdsEngineDeploymentFromRec(rec); engine != "" {
		return rdsPoolKey(rec.Region, rec.ResourceType, engine, deployment)
	}
	return poolKey(rec.Region, rec.ResourceType)
}

// rdsEngineDeploymentFromRec extracts the RDS engine and deployment
// strings from a recommendation's polymorphic Details, returning ("", "")
// when the rec isn't an RDS rec. Handles both pointer and value forms of
// DatabaseDetails because the live parser uses pointers and the CSV
// loader uses values.
func rdsEngineDeploymentFromRec(rec common.Recommendation) (engine, deployment string) {
	switch details := rec.Details.(type) {
	case *common.DatabaseDetails:
		if details != nil {
			return details.Engine, details.AZConfig
		}
	case common.DatabaseDetails:
		return details.Engine, details.AZConfig
	}
	return "", ""
}
