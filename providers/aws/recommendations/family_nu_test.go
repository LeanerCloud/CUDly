package recommendations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// TestRdsInstanceNUFromType covers the size→NU mapping for representative
// sizes plus the unknown-size fallback (returns 0 so callers know to skip
// family-NU sizing for that rec).
func TestRdsInstanceNUFromType(t *testing.T) {
	cases := map[string]float64{
		"db.r7g.large":    4,
		"db.r6g.2xlarge":  16,
		"db.t4g.medium":   2,
		"db.t4g.micro":    0.5,
		"db.r6g.16xlarge": 128,
		"db.r6g.24xlarge": 192,
		"db.r6g.unknown":  0, // fallback
		"not-an-rds-type": 0, // too few dots
		"":                0,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, rdsInstanceNUFromType(in))
		})
	}
}

// TestRdsFamilyFromType locks the family-prefix extraction so all sizes
// in the same family share a lookup key.
func TestRdsFamilyFromType(t *testing.T) {
	cases := map[string]string{
		"db.r7g.large":   "db.r7g",
		"db.r7g.2xlarge": "db.r7g",
		"db.t4g.medium":  "db.t4g",
		"db.m5.xlarge":   "db.m5",
		"db.m6gd.xlarge": "db.m6gd",
		"":               "",
		"db.r7g":         "", // missing size suffix
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, rdsFamilyFromType(in))
		})
	}
}

// TestAggregateRDSFamilyCoverage confirms per-pool coverage entries roll
// up to family-NU correctly: sum-of-(avg × size_NU) for TotalNU and
// sum-of-(avg × size_NU × pct/100) for CoveredNU. Mirrors the manual
// family-level table that proved size flex was implicit in AWS rec API.
func TestAggregateRDSFamilyCoverage(t *testing.T) {
	// Family: us-east-1 / db.r7g / Aurora MySQL / Single-AZ
	// Two sizes contributing:
	//   db.r7g.large  avg=40.6 cov=58%  → 162.4 NU running, 94.2 NU covered
	//   db.r7g.xlarge avg= 3.9 cov= 0%  →  31.2 NU running,    0 NU covered
	// Family totals: 193.6 NU running, 94.2 NU covered.
	cov := PoolCoverageMap{
		rdsPoolKey("us-east-1", "db.r7g.large", "Aurora MySQL", "Single-AZ"): {
			Pct: 58.0, AvgInstancesPerHour: 40.6,
		},
		rdsPoolKey("us-east-1", "db.r7g.xlarge", "Aurora MySQL", "Single-AZ"): {
			Pct: 0.0, AvgInstancesPerHour: 3.9,
		},
		// Different family in same region — should NOT roll into the r7g key.
		rdsPoolKey("us-east-1", "db.t4g.medium", "Aurora MySQL", "Single-AZ"): {
			Pct: 50.0, AvgInstancesPerHour: 100.0,
		},
		// Same instance, different deployment — distinct family bucket.
		rdsPoolKey("us-east-1", "db.r7g.large", "Aurora MySQL", "Multi-AZ"): {
			Pct: 0.0, AvgInstancesPerHour: 5.0,
		},
		// Non-RDS pool key (2-part) — should be skipped entirely.
		poolKey("us-east-1", "m5.large"): {Pct: 50.0, AvgInstancesPerHour: 10.0},
	}
	agg := AggregateRDSFamilyCoverage(cov)

	fk := rdsFamilyKey("us-east-1", "db.r7g", "Aurora MySQL", "Single-AZ")
	r7g := agg[fk]
	assert.InDelta(t, 40.6*4+3.9*8, r7g.TotalNU, 0.01, "TotalNU = sum(avg × NU(size))")
	assert.InDelta(t, 40.6*4*0.58+3.9*8*0.0, r7g.CoveredNU, 0.01, "CoveredNU = sum(avg × NU(size) × pct/100)")

	// Different deployment — separate bucket.
	multi := agg[rdsFamilyKey("us-east-1", "db.r7g", "Aurora MySQL", "Multi-AZ")]
	assert.InDelta(t, 5.0*4, multi.TotalNU, 0.01, "Multi-AZ keeps a separate family bucket")

	// Non-RDS key skipped.
	assert.NotContains(t, agg, "us-east-1:m5.large", "non-RDS pool keys should be ignored")
}

// TestApplyFamilyNUSizingRDS exercises the four important cases:
//  1. AWS rec at-target NU → scale = 1, no change
//  2. AWS rec under-recommends → scale > 1, counts grow
//  3. AWS rec over-recommends → scale < 1, counts shrink
//  4. Family already at target → all recs dropped
func TestApplyFamilyNUSizingRDS(t *testing.T) {
	t.Run("AWS rec NU matches target → counts unchanged", func(t *testing.T) {
		// Family db.r7g Aurora MySQL us-east-1:
		// TotalNU = 40.6*4 + 3.9*8 = 193.6; CoveredNU = 162.4*0.58 = 94.2
		// existing_pct = 48.66; gap = 80 − 48.66 = 31.34
		// target_NU = 31.34/100 × 193.6 ≈ 60.7
		// AWS rec: 15 × db.r7g.large = 60 NU → scale ≈ 1.01 → floor(15.1) = 15
		cov := PoolCoverageMap{
			rdsPoolKey("us-east-1", "db.r7g.large", "Aurora MySQL", "Single-AZ"): {
				Pct: 58.0, AvgInstancesPerHour: 40.6,
			},
			rdsPoolKey("us-east-1", "db.r7g.xlarge", "Aurora MySQL", "Single-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 3.9,
			},
		}
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "us-east-1",
				ResourceType:   "db.r7g.large",
				Count:          15,
				CommitmentCost: 1500, OnDemandCost: 3000, EstimatedSavings: 600,
				Details: &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
		}
		sized, nonRDS, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 1, "RDS rec kept (target NU > 0)")
		assert.Empty(t, nonRDS, "no non-RDS recs in this fixture")
		assert.Equal(t, 15, sized[0].Count, "AWS-rec NU already matches target → count preserved")
		// Costs unchanged (ratio = 1)
		assert.InDelta(t, 1500.0, sized[0].CommitmentCost, 0.01)
	})

	t.Run("RecurringMonthlyCost scales with count when populated", func(t *testing.T) {
		// Partial-upfront RIs carry a per-month fee in addition to upfront.
		// Family-NU sizing must scale this fee by newCount/oldCount so the
		// returned rec's monthly cost reflects what the user actually buys
		// (not AWS's original recommendation count). nil input stays nil.
		monthly := 100.0
		cov := PoolCoverageMap{
			rdsPoolKey("eu-west-2", "db.r7g.large", "MySQL", "Multi-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 10,
			},
		}
		recs := []common.Recommendation{
			{
				Service:              common.ServiceRDS,
				CommitmentType:       common.CommitmentReservedInstance,
				Region:               "eu-west-2",
				ResourceType:         "db.r7g.large",
				Count:                5,
				CommitmentCost:       5000,
				RecurringMonthlyCost: &monthly,
				Details:              &common.DatabaseDetails{Engine: "mysql", AZConfig: "multi-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 1)
		// Scale 32/20 = 1.6 → newCount = 8 → monthly = 100 × 8/5 = 160
		assert.Equal(t, 8, sized[0].Count)
		require.NotNil(t, sized[0].RecurringMonthlyCost)
		assert.InDelta(t, 160.0, *sized[0].RecurringMonthlyCost, 0.001, "monthly fee scales by 8/5 alongside other costs")
		assert.Equal(t, 100.0, monthly, "original target should not be mutated (new pointer)")
	})

	t.Run("AWS rec under-recommends → counts scale up", func(t *testing.T) {
		// Family eu-west-2 / db.r7g / MySQL / Multi-AZ:
		// Only db.r7g.large size in this fixture, avg=10, cov=0%.
		// TotalNU = 10*4 = 40; CoveredNU = 0; existing=0; gap=80 → target_NU = 32
		// AWS rec: 5 × db.r7g.large = 20 NU → scale = 32/20 = 1.6 → floor(8.0) = 8
		cov := PoolCoverageMap{
			rdsPoolKey("eu-west-2", "db.r7g.large", "MySQL", "Multi-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 10,
			},
		}
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "eu-west-2",
				ResourceType:   "db.r7g.large",
				Count:          5,
				CommitmentCost: 5000, OnDemandCost: 10000, EstimatedSavings: 2000,
				AverageInstancesUsedPerHour: 10,
				Details:                     &common.DatabaseDetails{Engine: "mysql", AZConfig: "multi-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 1)
		assert.Equal(t, 8, sized[0].Count, "scale 32/20 = 1.6 × 5 = 8 RIs to deliver 32 NU at 80% target")
		assert.InDelta(t, 5000*8.0/5.0, sized[0].CommitmentCost, 0.01, "CommitmentCost scales by 8/5")
	})

	t.Run("AWS rec over-recommends → counts scale down", func(t *testing.T) {
		// Family with low demand; AWS rec proposes more NU than 80% target needs.
		// TotalNU = 10*4 = 40; existing=0; target=80 → target_NU = 32
		// AWS rec: 20 × .large = 80 NU → scale = 32/80 = 0.4 → floor(8.0) = 8
		cov := PoolCoverageMap{
			rdsPoolKey("eu-west-2", "db.r7g.large", "MySQL", "Multi-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 10,
			},
		}
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "eu-west-2",
				ResourceType:   "db.r7g.large",
				Count:          20,
				CommitmentCost: 20000,
				Details:        &common.DatabaseDetails{Engine: "mysql", AZConfig: "multi-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 1)
		assert.Equal(t, 8, sized[0].Count, "AWS over-proposed; scale down to family target")
	})

	t.Run("family at-or-above target → all recs dropped", func(t *testing.T) {
		// existing_pct = 90% > target 80% → drop.
		cov := PoolCoverageMap{
			rdsPoolKey("us-east-1", "db.r7g.large", "Aurora MySQL", "Single-AZ"): {
				Pct: 90.0, AvgInstancesPerHour: 10,
			},
		}
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "us-east-1",
				ResourceType:   "db.r7g.large",
				Count:          5,
				Details:        &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		assert.Empty(t, sized, "family already covered → drop all recs")
	})

	t.Run("no coverage signal → recs pass through unchanged", func(t *testing.T) {
		// Empty coverage map → rec.Count untouched, rec returned in sizedRDS
		// (caller treats as already-sized to avoid double-sizing).
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "ap-east-1",
				ResourceType:   "db.r7g.large",
				Count:          5,
				Details:        &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, PoolCoverageMap{}, 80)
		require.Len(t, sized, 1, "rec preserved as-is when no family-NU signal")
		assert.Equal(t, 5, sized[0].Count)
	})

	t.Run("non-RDS recs flow through nonRDS partition", func(t *testing.T) {
		// EC2 and SP recs should NOT be touched by family-NU; they appear
		// in the nonRDS slice for per-pool sizing.
		recs := []common.Recommendation{
			{Service: common.ServiceEC2, CommitmentType: common.CommitmentReservedInstance, Count: 3},
			{Service: common.ServiceSavingsPlans, CommitmentType: common.CommitmentSavingsPlan, Count: 1},
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "us-east-1",
				ResourceType:   "db.r7g.large",
				Count:          5,
				Details:        &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
		}
		cov := PoolCoverageMap{
			rdsPoolKey("us-east-1", "db.r7g.large", "Aurora MySQL", "Single-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 10,
			},
		}
		sized, nonRDS, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 1, "only the RDS rec went through family-NU")
		require.Len(t, nonRDS, 2, "EC2 + SP recs left for per-pool sizing")
		assert.Equal(t, common.ServiceEC2, nonRDS[0].Service)
		assert.Equal(t, common.ServiceSavingsPlans, nonRDS[1].Service)
	})

	t.Run("ProjectedCoverage is cumulative across recs in a family", func(t *testing.T) {
		// Two per-account recs in same family/engine/deployment: each rec
		// must show the family-wide projected coverage (existing + sum-of-
		// all-recs' new NU / family total NU), not just its own slice.
		// Without cumulation, two recs each adding 20% of family NU would
		// each report "existing + 20%" instead of the true "existing + 40%".
		cov := PoolCoverageMap{
			rdsPoolKey("us-east-1", "db.t4g.medium", "Aurora PostgreSQL", "Single-AZ"): {
				Pct: 16.7, AvgInstancesPerHour: 12.0, // 24 NU at .medium (×2 NU)
			},
		}
		// Family total NU = 24. AWS rec api returned two recs (per account)
		// each .medium size; AWS-implied total = 5+3 = 8 RIs = 16 NU.
		// existing=16.7%, gap=63.3, targetNU = 63.3/100 × 24 = 15.2.
		// scale = 15.2/16 = 0.95 → floor(5×0.95)=4, floor(3×0.95)=2.
		// Total new NU = (4+2) × 2 = 12. Cumulative projection =
		// 16.7 + 12/24*100 = 16.7 + 50 = 66.7%.
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "us-east-1",
				ResourceType:   "db.t4g.medium",
				Account:        "production",
				Count:          5,
				CommitmentCost: 500,
				Details:        &common.DatabaseDetails{Engine: "aurora-postgresql", AZConfig: "single-az"},
			},
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "us-east-1",
				ResourceType:   "db.t4g.medium",
				Account:        "staging",
				Count:          3,
				CommitmentCost: 300,
				Details:        &common.DatabaseDetails{Engine: "aurora-postgresql", AZConfig: "single-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 2, "both recs kept after scaling")
		// Both recs see the SAME cumulative projection.
		assert.InDelta(t, sized[0].ProjectedCoverage, sized[1].ProjectedCoverage, 0.001,
			"both recs in the same family must report the same ProjectedCoverage")
		// And the projection reflects BOTH recs' contributions, not just
		// either one alone. existing 16.7 + (4+2)*2 / 24 * 100 = 66.7
		assert.InDelta(t, 66.7, sized[0].ProjectedCoverage, 0.01,
			"cumulative projection: existing + sum-of-all-recs new NU / family total NU")
		// Both rec.Count values reflect the scale-down.
		assert.Equal(t, 4, sized[0].Count, "prod scaled 5→4")
		assert.Equal(t, 2, sized[1].Count, "staging scaled 3→2")
	})

	t.Run("multiple sizes in same family scale together", func(t *testing.T) {
		// Family db.r6g Aurora MySQL eu-west-2 has demand at .large and .xlarge.
		// avg .large = 5 (20 NU), avg .xlarge = 5 (40 NU). Total = 60 NU; cov = 0.
		// target_NU @ 80% = 48.
		// AWS rec: 10 × .large = 40 NU. scale = 48/40 = 1.2 → floor(12.0) = 12 .large.
		// (No AWS rec at .xlarge — the bundled .large covers via size flex.)
		cov := PoolCoverageMap{
			rdsPoolKey("eu-west-2", "db.r6g.large", "Aurora MySQL", "Single-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 5,
			},
			rdsPoolKey("eu-west-2", "db.r6g.xlarge", "Aurora MySQL", "Single-AZ"): {
				Pct: 0.0, AvgInstancesPerHour: 5,
			},
		}
		recs := []common.Recommendation{
			{
				Service:        common.ServiceRDS,
				CommitmentType: common.CommitmentReservedInstance,
				Region:         "eu-west-2",
				ResourceType:   "db.r6g.large",
				Count:          10,
				Details:        &common.DatabaseDetails{Engine: "aurora-mysql", AZConfig: "single-az"},
			},
		}
		sized, _, _ := ApplyFamilyNUSizingRDS(recs, cov, 80)
		require.Len(t, sized, 1)
		assert.Equal(t, 12, sized[0].Count, "family-NU need includes .xlarge demand even though AWS rec is at .large")
	})
}

// TestIsRDSRIRec covers the dispatch predicate that gates family-NU
// sizing — must reject SP/non-RDS commitments so they reach per-pool
// sizing untouched.
func TestIsRDSRIRec(t *testing.T) {
	cases := []struct {
		name string
		rec  common.Recommendation
		want bool
	}{
		{"RDS RI", common.Recommendation{Service: common.ServiceRDS, CommitmentType: common.CommitmentReservedInstance}, true},
		{"RelationalDB alias", common.Recommendation{Service: common.ServiceRelationalDB, CommitmentType: common.CommitmentReservedInstance}, true},
		{"EC2 RI", common.Recommendation{Service: common.ServiceEC2, CommitmentType: common.CommitmentReservedInstance}, false},
		{"RDS SP (impossible but defensive)", common.Recommendation{Service: common.ServiceRDS, CommitmentType: common.CommitmentSavingsPlan}, false},
		{"SP", common.Recommendation{Service: common.ServiceSavingsPlans, CommitmentType: common.CommitmentSavingsPlan}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isRDSRIRec(tc.rec))
		})
	}
}
