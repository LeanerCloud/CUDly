package recommendations

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// mockCoverageCE extends the test mock with a configurable GetReservationCoverage
// response so the coverage path can be exercised without hitting AWS.
// lastTimePeriod captures the TimePeriod from the most recent call so tests can
// assert that the CE window matches the requested lookbackDays.
type mockCoverageCE struct {
	mockCostExplorerAPI
	coverageOutput *costexplorer.GetReservationCoverageOutput
	coverageError  error
	coverageCalls  int
	lastTimePeriod *types.DateInterval
}

func (m *mockCoverageCE) GetReservationCoverage(ctx context.Context, params *costexplorer.GetReservationCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationCoverageOutput, error) {
	m.coverageCalls++
	if params != nil {
		m.lastTimePeriod = params.TimePeriod
	}
	if m.coverageError != nil {
		return nil, m.coverageError
	}
	return m.coverageOutput, nil
}

// TestGetRICoverageMap_GroupsByInstanceType confirms a single-page CE
// response is parsed into the expected pool-keyed map and that absent
// attribute keys are skipped rather than producing zero-valued entries.
// Coverage is org-wide (no account dimension) — matches the AWS console
// reservations-coverage report shape.
func TestGetRICoverageMap_GroupsByInstanceType(t *testing.T) {
	mock := &mockCoverageCE{
		coverageOutput: &costexplorer.GetReservationCoverageOutput{
			CoveragesByTime: []types.CoverageByTime{
				{
					Groups: []types.ReservationCoverageGroup{
						{
							Attributes: map[string]string{
								"instanceType": "db.r6g.large",
							},
							Coverage: &types.Coverage{
								CoverageHours: &types.CoverageHours{
									CoverageHoursPercentage: aws.String("50.0"),
									// 720h in a 30-day window = avg 1 instance.
									TotalRunningHours: aws.String("720"),
								},
							},
						},
						{
							// SCREAMING_SNAKE_CASE fallback path: CE returns
							// camelCase keys in practice but the parser tolerates
							// either form.
							Attributes: map[string]string{
								"INSTANCE_TYPE": "db.m5.xlarge",
							},
							Coverage: &types.Coverage{
								CoverageHours: &types.CoverageHours{
									CoverageHoursPercentage: aws.String("33.7"),
									// 7200h in a 30-day window = avg 10 instances.
									TotalRunningHours: aws.String("7200"),
								},
							},
						},
						{
							// Missing INSTANCE_TYPE attribute → skipped.
							Attributes: map[string]string{},
							Coverage: &types.Coverage{
								CoverageHours: &types.CoverageHours{
									CoverageHoursPercentage: aws.String("99.9"),
								},
							},
						},
					},
				},
			},
		},
	}
	client := NewClientWithAPI(mock, "us-east-1")

	regions := []string{"us-east-1", "eu-west-2"}
	got, err := client.GetRICoverageMap(context.Background(), 30, regions)
	require.NoError(t, err)
	// One call per (region, service) for non-RDS + one per (region, RDS engine).
	// Total = len(regions) × (len(coverageServiceFilters) + len(rdsCoverageEngines)).
	wantCalls := len(regions) * (len(coverageServiceFilters) + len(rdsCoverageEngines))
	assert.Equal(t, wantCalls, mock.coverageCalls, "one CE call per (region, service|engine) combo")

	// us-east-1 + db.r6g.large = 50% / avg 1. The mock returns the same
	// canned response for every call, so the per-region loop writes the
	// same key on every iteration — last write wins.
	r6gLarge := got[poolKey("eu-west-2", "db.r6g.large")]
	assert.InDelta(t, 50.0, r6gLarge.Pct, 0.001)
	assert.InDelta(t, 1.0, r6gLarge.AvgInstancesPerHour, 0.001, "TotalRunningHours / window hours = avg concurrent instances")
	m5xLarge := got[poolKey("eu-west-2", "db.m5.xlarge")]
	assert.InDelta(t, 33.7, m5xLarge.Pct, 0.001, "SCREAMING_SNAKE_CASE attribute keys tolerated")
	assert.InDelta(t, 10.0, m5xLarge.AvgInstancesPerHour, 0.001, "avg parsed regardless of attribute-key casing")
	_, hasMissing := got[poolKey("us-east-1", "")]
	assert.False(t, hasMissing, "groups missing INSTANCE_TYPE should be skipped, not emit empty keys")
}

// TestGetRICoverageMap_LookbackDefault confirms that a non-positive lookback
// substitutes the 30-day default (matches GetRIUtilization's behaviour).
func TestGetRICoverageMap_LookbackDefault(t *testing.T) {
	mock := &mockCoverageCE{coverageOutput: &costexplorer.GetReservationCoverageOutput{}}
	client := NewClientWithAPI(mock, "us-east-1")

	regions := []string{"us-east-1"}
	_, err := client.GetRICoverageMap(context.Background(), 0, regions)
	require.NoError(t, err)
	wantCalls := len(regions) * (len(coverageServiceFilters) + len(rdsCoverageEngines))
	assert.Equal(t, wantCalls, mock.coverageCalls)
}

// TestGetRICoverageMap_LookbackWindowWidth asserts that the CE TimePeriod
// sent to GetReservationCoverage spans exactly lookbackDays calendar days.
// This test is deliberately discriminating: it parses the YYYY-MM-DD Start
// and End strings and verifies end-start == N days so a regression in the
// window math (e.g. reverting to a hardcoded 30) causes a test failure.
func TestGetRICoverageMap_LookbackWindowWidth(t *testing.T) {
	cases := []struct {
		name         string
		lookbackDays int
	}{
		{"14-day window", 14},
		{"60-day window", 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCoverageCE{coverageOutput: &costexplorer.GetReservationCoverageOutput{}}
			client := NewClientWithAPI(mock, "us-east-1")

			before := time.Now().UTC()
			_, err := client.GetRICoverageMap(context.Background(), tc.lookbackDays, []string{"us-east-1"})
			after := time.Now().UTC()
			require.NoError(t, err)
			require.NotNil(t, mock.lastTimePeriod, "GetReservationCoverage must have been called with a TimePeriod")
			require.NotNil(t, mock.lastTimePeriod.Start)
			require.NotNil(t, mock.lastTimePeriod.End)

			start, err := time.Parse("2006-01-02", aws.ToString(mock.lastTimePeriod.Start))
			require.NoError(t, err, "TimePeriod.Start must be a valid YYYY-MM-DD date")
			end, err := time.Parse("2006-01-02", aws.ToString(mock.lastTimePeriod.End))
			require.NoError(t, err, "TimePeriod.End must be a valid YYYY-MM-DD date")

			// CE dates are truncated to day; allow for midnight-boundary
			// skew of at most 1 day when the test runs near UTC midnight.
			spanDays := int(end.Sub(start).Hours() / 24)
			assert.Equal(t, tc.lookbackDays, spanDays,
				"CE TimePeriod must span exactly lookbackDays days (start=%s end=%s)",
				aws.ToString(mock.lastTimePeriod.Start),
				aws.ToString(mock.lastTimePeriod.End))

			// Sanity: the End date must fall within the test's execution window.
			endUTC := end
			assert.True(t, !endUTC.Before(before.Truncate(24*time.Hour)),
				"TimePeriod.End must not be before test start")
			assert.True(t, !endUTC.After(after.Add(24*time.Hour)),
				"TimePeriod.End must not be more than a day after test completion")
		})
	}
}

// TestApplyCoverageMapToRecommendations covers the org-wide pool matching:
// recs look up by (region, instance_type, [engine, deployment]) so any
// linked account in the org sees the same coverage % for the same pool
// (matches AWS console aggregation).
func TestApplyCoverageMapToRecommendations(t *testing.T) {
	recs := []common.Recommendation{
		{Region: "us-east-1", ResourceType: "db.r6g.large", Account: "acct-a"},
		{Region: "eu-west-2", ResourceType: "db.r6g.large", Account: "acct-b"},
		{Region: "us-east-1", ResourceType: "db.m5.large", Account: "acct-a"}, // no match
	}
	coverage := PoolCoverageMap{
		poolKey("us-east-1", "db.r6g.large"): {Pct: 50.0},
		poolKey("eu-west-2", "db.r6g.large"): {Pct: 33.7},
	}

	ApplyCoverageMapToRecommendations(recs, coverage)

	assert.InDelta(t, 50.0, recs[0].ExistingCoveragePct, 0.001)
	assert.True(t, recs[0].ExistingCoverageKnown, "matched pool sets Known=true")
	assert.InDelta(t, 33.7, recs[1].ExistingCoveragePct, 0.001)
	assert.True(t, recs[1].ExistingCoverageKnown, "matched pool sets Known=true")
	assert.Equal(t, 0.0, recs[2].ExistingCoveragePct, "unmatched pool should leave field at zero (no signal)")
	assert.False(t, recs[2].ExistingCoverageKnown, "unmatched pool leaves Known=false so CSV renders n/a")

	t.Run("empty map is a no-op", func(t *testing.T) {
		recs := []common.Recommendation{
			{Region: "us-east-1", ResourceType: "db.r6g.large", ExistingCoveragePct: 42},
		}
		ApplyCoverageMapToRecommendations(recs, nil)
		assert.Equal(t, 42.0, recs[0].ExistingCoveragePct, "nil map must leave existing values untouched")
	})

	t.Run("case-insensitive matching for region/instance", func(t *testing.T) {
		// Recommendations carry mixed-case region/type strings; the lookup
		// must normalise both sides via poolKey.
		recs := []common.Recommendation{{Region: "US-EAST-1", ResourceType: "DB.R6G.LARGE"}}
		cov := PoolCoverageMap{poolKey("us-east-1", "db.r6g.large"): {Pct: 75.0}}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 75.0, recs[0].ExistingCoveragePct, 0.001)
	})

	t.Run("all accounts in same pool see the same coverage", func(t *testing.T) {
		// Pools are now org-wide: prod and staging running the same
		// instance type / engine / deployment in the same region see the
		// same coverage % (matches AWS console aggregation across linked
		// accounts). Whoever buys the RI covers the org-wide pool.
		recs := []common.Recommendation{
			{
				Service:      common.ServiceRDS,
				Region:       "us-east-1",
				ResourceType: "db.t4g.medium",
				Account:      "prod-account",
				Details:      &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
			{
				Service:      common.ServiceRDS,
				Region:       "us-east-1",
				ResourceType: "db.t4g.medium",
				Account:      "staging-account",
				Details:      &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
		}
		cov := PoolCoverageMap{
			rdsPoolKey("us-east-1", "db.t4g.medium", "Aurora MySQL", "Single-AZ"): {Pct: 55.0},
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 55.0, recs[0].ExistingCoveragePct, 0.001, "prod sees the org-wide 55% coverage")
		assert.InDelta(t, 55.0, recs[1].ExistingCoveragePct, 0.001, "staging sees the same org-wide 55% coverage")
	})

	t.Run("avg-per-pool rebalances to coverage's org-wide signal", func(t *testing.T) {
		// Single-rec-per-pool case: rec.avg is replaced with cov.avg via
		// scale = cov.avg / rec.avg. Whatever AWS rec API surfaced as the
		// per-account avg is replaced with the coverage's org-wide avg so
		// downstream sizing buys the right number for the whole pool.
		recs := []common.Recommendation{
			// rec[0]: no per-account avg; coverage provides one → recs[0].avg = cov.avg
			{Region: "us-east-1", ResourceType: "m5.large"},
			// rec[1]: per-account avg present; coverage's larger avg now overrides
			{Region: "us-east-1", ResourceType: "m5.xlarge", AverageInstancesUsedPerHour: 5.0},
		}
		cov := PoolCoverageMap{
			poolKey("us-east-1", "m5.large"):  {Pct: 50.0, AvgInstancesPerHour: 10.0},
			poolKey("us-east-1", "m5.xlarge"): {Pct: 30.0, AvgInstancesPerHour: 99.0},
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 10.0, recs[0].AverageInstancesUsedPerHour, 0.001, "no-signal rec gets coverage's org-wide avg")
		assert.InDelta(t, 99.0, recs[1].AverageInstancesUsedPerHour, 0.001, "single rec in pool: avg replaced with coverage's org-wide avg (scale=99/5)")
	})

	t.Run("avg-per-pool splits proportionally across multiple recs", func(t *testing.T) {
		// Two per-account recs in the same pool with avgs 24.4 and 23.2;
		// CE coverage reports org-wide avg of 210 for the pool (other
		// accounts not surfaced by AWS rec API). Each rec gets scaled by
		// 210 / (24.4+23.2) ≈ 4.412 so the per-pool sum hits 210, which
		// is what the AWS console coverage CSV would target.
		recs := []common.Recommendation{
			{
				Service:                     common.ServiceRDS,
				Region:                      "us-east-1",
				ResourceType:                "db.t4g.medium",
				Account:                     "production",
				Details:                     &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
				AverageInstancesUsedPerHour: 24.4,
			},
			{
				Service:                     common.ServiceRDS,
				Region:                      "us-east-1",
				ResourceType:                "db.t4g.medium",
				Account:                     "staging",
				Details:                     &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
				AverageInstancesUsedPerHour: 23.2,
			},
		}
		cov := PoolCoverageMap{
			rdsPoolKey("us-east-1", "db.t4g.medium", "Aurora MySQL", "Single-AZ"): {
				Pct: 55.0, AvgInstancesPerHour: 210.0,
			},
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		// Per-rec scaled avgs sum to the coverage's org-wide avg.
		sum := recs[0].AverageInstancesUsedPerHour + recs[1].AverageInstancesUsedPerHour
		assert.InDelta(t, 210.0, sum, 0.001, "scaled per-rec avgs sum to coverage's org-wide avg")
		// Proportions preserved: prod (24.4 / 47.6 = 51.3%) of the org total.
		assert.InDelta(t, 210.0*24.4/47.6, recs[0].AverageInstancesUsedPerHour, 0.001, "prod gets its proportional share of org-wide avg")
		assert.InDelta(t, 210.0*23.2/47.6, recs[1].AverageInstancesUsedPerHour, 0.001, "staging gets its proportional share of org-wide avg")
	})

	t.Run("avg-per-pool splits evenly when every rec has zero avg", func(t *testing.T) {
		// Two recs in same pool, both with zero per-account avg signal.
		// No per-rec proportion to preserve; coverage's avg splits evenly.
		recs := []common.Recommendation{
			{Region: "us-east-1", ResourceType: "m5.large", Account: "a"},
			{Region: "us-east-1", ResourceType: "m5.large", Account: "b"},
		}
		cov := PoolCoverageMap{
			poolKey("us-east-1", "m5.large"): {Pct: 50.0, AvgInstancesPerHour: 10.0},
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 5.0, recs[0].AverageInstancesUsedPerHour, 0.001, "even split across zero-signal recs in same pool")
		assert.InDelta(t, 5.0, recs[1].AverageInstancesUsedPerHour, 0.001, "even split across zero-signal recs in same pool")
	})

	t.Run("avg-per-pool leaves rec.avg untouched when coverage has no avg signal", func(t *testing.T) {
		// Coverage entry exists (sets ExistingCoveragePct) but has no avg
		// signal (e.g., TotalRunningHours absent from CE response). The
		// per-account avg is the only signal — leave it alone.
		recs := []common.Recommendation{
			{Region: "us-east-1", ResourceType: "m5.large", AverageInstancesUsedPerHour: 7.5},
		}
		cov := PoolCoverageMap{
			poolKey("us-east-1", "m5.large"): {Pct: 50.0, AvgInstancesPerHour: 0},
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 50.0, recs[0].ExistingCoveragePct, 0.001)
		assert.InDelta(t, 7.5, recs[0].AverageInstancesUsedPerHour, 0.001, "no coverage avg signal → leave rec avg as-is")
	})

	t.Run("RDS recs look up with engine + deployment-aware key", func(t *testing.T) {
		// Same region + instance type, different engines and deployments —
		// the per-engine fetcher writes one entry per (engine, deployment)
		// and the lookup must pick the right one. CE-side ("Aurora MySQL",
		// "Single-AZ") and parser-side ("aurora-mysql", "single-az") forms
		// collapse to the same key.
		recs := []common.Recommendation{
			{
				Service:      common.ServiceRDS,
				Region:       "eu-west-2",
				ResourceType: "db.r6g.large",
				Details:      &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
			{
				Service:      common.ServiceRDS,
				Region:       "eu-west-2",
				ResourceType: "db.r6g.large",
				Details:      &common.DatabaseDetails{Engine: "mysql", AZConfig: "multi-az"},
			},
			{
				// Same engine as rec[0] but Multi-AZ — a different pool
				// scope (a Single-AZ RI can't cover Multi-AZ demand).
				Service:      common.ServiceRDS,
				Region:       "eu-west-2",
				ResourceType: "db.r6g.large",
				Details:      &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "multi-az"},
			},
		}
		cov := PoolCoverageMap{
			rdsPoolKey("eu-west-2", "db.r6g.large", "Aurora MySQL", "Single-AZ"): {Pct: 98.5},
			rdsPoolKey("eu-west-2", "db.r6g.large", "MySQL", "Multi-AZ"):         {Pct: 0.0},
			rdsPoolKey("eu-west-2", "db.r6g.large", "Aurora MySQL", "Multi-AZ"):  {Pct: 12.0},
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 98.5, recs[0].ExistingCoveragePct, 0.001, "aurora-mysql single-az rec picks up its own coverage")
		assert.Equal(t, 0.0, recs[1].ExistingCoveragePct, "mysql multi-az rec sees only its own coverage")
		assert.InDelta(t, 12.0, recs[2].ExistingCoveragePct, 0.001, "aurora-mysql multi-az is a distinct pool from single-az")
	})
}

// TestNormaliseRDSEngine locks the canonicalisation of CE-side and
// parser-side engine strings to the same lookup key. Without this both
// producer (per-engine fetcher) and consumer (apply helper) would write
// or read differently and miss the lookup entirely.
func TestNormaliseRDSEngine(t *testing.T) {
	cases := map[string]string{
		// CE-side strings (human readable, mixed case, spaces).
		"Aurora MySQL":      "auroramysql",
		"Aurora PostgreSQL": "aurorapostgresql",
		"MySQL":             "mysql",
		"PostgreSQL":        "postgresql",
		"SQL Server":        "sqlserver",
		// Parser-side strings (lowercase, hyphenated).
		"aurora-mysql":      "auroramysql",
		"aurora-postgresql": "aurorapostgresql",
		// "postgres" is the bare RDS engine slug that DescribeDBInstances
		// returns; CE coverage emits keys under "postgresql" (the display
		// form). Collapse so both sides land on the same lookup key.
		"postgres":   "postgresql",
		"POSTGRES":   "postgresql",
		"postgresql": "postgresql",
		// SQL Server edition slugs (Express / Web / Standard / Enterprise)
		// collapse to the bare family; CE coverage emits keys under
		// "sqlserver" (from the "SQL Server" display form already covered
		// in the CE-side block above).
		"sqlserver-ee":  "sqlserver",
		"sqlserver-se":  "sqlserver",
		"sqlserver-ex":  "sqlserver",
		"sqlserver-web": "sqlserver",
		// Oracle edition slugs collapse to the bare family too.
		"oracle-ee":  "oracle",
		"oracle-se":  "oracle",
		"oracle-se1": "oracle",
		"oracle-se2": "oracle",
		// Edge cases.
		"":      "",
		"MYSQL": "mysql",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, normaliseRDSEngine(in))
		})
	}
}

// TestFetchCoveragePaged_CtxCancelReturnsError asserts that a cancelled context
// is treated as a hard stop inside the pagination loop and surfaces an error
// rather than returning a partial/empty result silently
// (feedback_ctx_cancel_terminal).
func TestFetchCoveragePaged_CtxCancelReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	// The mock returns a non-nil token on the first call so the loop
	// would try to paginate -- but ctx.Err() must fire first.
	token := "page2"
	mock := &mockCoverageCE{
		coverageOutput: &costexplorer.GetReservationCoverageOutput{
			NextPageToken: &token,
		},
	}
	client := NewClientWithAPI(mock, "us-east-1")

	err := client.fetchCoveragePaged(
		ctx,
		&costexplorer.GetReservationCoverageInput{},
		func(_, _ string, _ PoolCoverage) {},
		720,
	)
	require.Error(t, err, "cancelled context must surface an error from fetchCoveragePaged")
}

// TestNormaliseDeployment locks deployment-option canonicalisation. CE
// returns "Single-AZ" / "Multi-AZ" / "Multi-AZ (readable standbys)" while
// the parser stores "single-az" / "multi-az"; both forms must collapse to
// the same lookup key.
func TestNormaliseDeployment(t *testing.T) {
	cases := map[string]string{
		"Single-AZ":                    "singleaz",
		"single-az":                    "singleaz",
		"Multi-AZ":                     "multiaz",
		"multi-az":                     "multiaz",
		"Multi-AZ (readable standbys)": "multiazreadablestandbys",
		"":                             "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, normaliseDeployment(in))
		})
	}
}
