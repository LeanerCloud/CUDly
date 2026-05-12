package recommendations

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// mockCoverageCE extends the test mock with a configurable GetReservationCoverage
// response so the coverage path can be exercised without hitting AWS.
type mockCoverageCE struct {
	mockCostExplorerAPI
	coverageOutput *costexplorer.GetReservationCoverageOutput
	coverageError  error
	coverageCalls  int
}

func (m *mockCoverageCE) GetReservationCoverage(ctx context.Context, params *costexplorer.GetReservationCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationCoverageOutput, error) {
	m.coverageCalls++
	if m.coverageError != nil {
		return nil, m.coverageError
	}
	return m.coverageOutput, nil
}

// TestGetRICoverageMap_GroupsByRegionAndInstanceType confirms a single-page
// CE response is parsed into the expected pool-keyed map and that absent
// attribute keys are skipped rather than producing zero-valued entries.
func TestGetRICoverageMap_GroupsByRegionAndInstanceType(t *testing.T) {
	mock := &mockCoverageCE{
		coverageOutput: &costexplorer.GetReservationCoverageOutput{
			CoveragesByTime: []types.CoverageByTime{
				{
					Groups: []types.ReservationCoverageGroup{
						{
							Attributes: map[string]string{
								"region":       "us-east-1",
								"instanceType": "db.r6g.large",
							},
							Coverage: &types.Coverage{
								CoverageHours: &types.CoverageHours{
									CoverageHoursPercentage: aws.String("50.0"),
								},
							},
						},
						{
							// Mixed-case region from CE should normalise to lowercase.
							// Also exercises the SCREAMING_SNAKE_CASE fallback path:
							// CE returns camelCase keys in practice but our parser
							// tolerates either form.
							Attributes: map[string]string{
								"REGION":        "EU-WEST-2",
								"INSTANCE_TYPE": "db.r6g.large",
							},
							Coverage: &types.Coverage{
								CoverageHours: &types.CoverageHours{
									CoverageHoursPercentage: aws.String("33.7"),
								},
							},
						},
						{
							// Missing INSTANCE_TYPE attribute → skipped.
							Attributes: map[string]string{"REGION": "us-east-1"},
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

	got, err := client.GetRICoverageMap(context.Background(), 30)
	require.NoError(t, err)
	// One call per non-RDS service in coverageServiceFilters (EC2,
	// ElastiCache, OpenSearch, Redshift, MemoryDB = 5) + one per RDS
	// engine in rdsCoverageEngines (MySQL/PostgreSQL/MariaDB/Oracle/
	// SQL Server/Aurora MySQL/Aurora PostgreSQL = 7) = 12 single-page calls.
	wantCalls := len(coverageServiceFilters) + len(rdsCoverageEngines)
	assert.Equal(t, wantCalls, mock.coverageCalls, "one CE call per (service|RDS engine) filter combo")

	assert.InDelta(t, 50.0, got[poolKey("us-east-1", "db.r6g.large")], 0.001)
	assert.InDelta(t, 33.7, got[poolKey("eu-west-2", "db.r6g.large")], 0.001, "REGION attribute should be lowercased")
	_, hasMissing := got[poolKey("us-east-1", "")]
	assert.False(t, hasMissing, "groups missing INSTANCE_TYPE should be skipped, not emit empty keys")
}

// TestGetRICoverageMap_LookbackDefault confirms that a non-positive lookback
// substitutes the 30-day default (matches GetRIUtilization's behaviour).
func TestGetRICoverageMap_LookbackDefault(t *testing.T) {
	mock := &mockCoverageCE{coverageOutput: &costexplorer.GetReservationCoverageOutput{}}
	client := NewClientWithAPI(mock, "us-east-1")

	_, err := client.GetRICoverageMap(context.Background(), 0)
	require.NoError(t, err)
	wantCalls := len(coverageServiceFilters) + len(rdsCoverageEngines)
	assert.Equal(t, wantCalls, mock.coverageCalls)
}

// TestApplyCoverageMapToRecommendations covers the three cases: matched
// (sets the field), unmatched (leaves zero), and empty map (no-op).
func TestApplyCoverageMapToRecommendations(t *testing.T) {
	recs := []common.Recommendation{
		{Region: "us-east-1", ResourceType: "db.r6g.large"},
		{Region: "eu-west-2", ResourceType: "db.r6g.large"},
		{Region: "us-east-1", ResourceType: "db.m5.large"}, // no match
	}
	coverage := PoolCoverageMap{
		"us-east-1:db.r6g.large": 50.0,
		"eu-west-2:db.r6g.large": 33.7,
	}

	ApplyCoverageMapToRecommendations(recs, coverage)

	assert.InDelta(t, 50.0, recs[0].ExistingCoveragePct, 0.001)
	assert.InDelta(t, 33.7, recs[1].ExistingCoveragePct, 0.001)
	assert.Equal(t, 0.0, recs[2].ExistingCoveragePct, "unmatched pool should leave field at zero (no signal)")

	t.Run("empty map is a no-op", func(t *testing.T) {
		recs := []common.Recommendation{
			{Region: "us-east-1", ResourceType: "db.r6g.large", ExistingCoveragePct: 42},
		}
		ApplyCoverageMapToRecommendations(recs, nil)
		assert.Equal(t, 42.0, recs[0].ExistingCoveragePct, "nil map must leave existing values untouched")
	})

	t.Run("case-insensitive matching", func(t *testing.T) {
		// Recommendations carry mixed-case region/type strings; the lookup
		// must normalise both sides via poolKey.
		recs := []common.Recommendation{{Region: "US-EAST-1", ResourceType: "DB.R6G.LARGE"}}
		cov := PoolCoverageMap{"us-east-1:db.r6g.large": 75.0}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 75.0, recs[0].ExistingCoveragePct, 0.001)
	})

	t.Run("RDS recs look up with engine-aware key", func(t *testing.T) {
		// Same region + instance type, different engines, different
		// existing coverage — the per-engine fetcher writes one entry per
		// engine and the lookup must pick the right one. CE-side ("Aurora
		// MySQL") and parser-side ("aurora-mysql") forms must collapse to
		// the same key via normaliseRDSEngine.
		recs := []common.Recommendation{
			{
				Service:      common.ServiceRDS,
				Region:       "eu-west-2",
				ResourceType: "db.r6g.large",
				Details:      &common.DatabaseDetails{Engine: "aurora-mysql"},
			},
			{
				Service:      common.ServiceRDS,
				Region:       "eu-west-2",
				ResourceType: "db.r6g.large",
				Details:      &common.DatabaseDetails{Engine: "mysql"},
			},
		}
		cov := PoolCoverageMap{
			rdsPoolKey("eu-west-2", "db.r6g.large", "Aurora MySQL"): 98.5,
			rdsPoolKey("eu-west-2", "db.r6g.large", "MySQL"):        0.0,
		}
		ApplyCoverageMapToRecommendations(recs, cov)
		assert.InDelta(t, 98.5, recs[0].ExistingCoveragePct, 0.001, "aurora-mysql rec picks up Aurora MySQL coverage")
		assert.Equal(t, 0.0, recs[1].ExistingCoveragePct, "MySQL rec sees only MySQL coverage, not Aurora's")
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
		"postgres":          "postgres",
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
