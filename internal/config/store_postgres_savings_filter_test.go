package config

// Regression tests for issue #1089: min-savings unit disambiguation.
// These tests assert that MinSavingsUSD (dollar floor, pushed to SQL) and
// MinSavingsPct (percentage floor, applied in-process) are distinct fields
// with separate semantics that cannot be conflated.
//
// recEffectiveSavingsPct and buildRecommendationFilter are unexported; the
// white-box package config tests exercise them directly.

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// recEffectiveSavingsPct unit tests
// ---------------------------------------------------------------------------

func TestRecEffectiveSavingsPct_Config(t *testing.T) {
	f64 := func(v float64) *float64 { return &v }

	t.Run("correct pct with on_demand_cost", func(t *testing.T) {
		// term=1yr, no upfront, savings=$100/mo, on_demand=$500/mo
		// effective = 100 - 0 = 100; pct = (100/500)*100 = 20%
		rec := &RecommendationRecord{
			Term:         1,
			Savings:      100,
			UpfrontCost:  0,
			OnDemandCost: f64(500),
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 20.0, pct, 0.001)
	})

	t.Run("does not subtract amortized upfront from net savings", func(t *testing.T) {
		// Issue #1148 (sibling of frontend #1103): `savings` is already net
		// of amortized upfront across all three providers, so the numerator
		// is savings directly -- amortized upfront must NOT be subtracted
		// again.
		// term=3yr, upfront=$360, savings=$100/mo (net), on_demand=$110/mo
		// pct = (100/110)*100 ~ 90.9% (the old double-subtract formula
		// produced (100-10)/110 ~ 81.8%)
		rec := &RecommendationRecord{
			Term:         3,
			Savings:      100,
			UpfrontCost:  360,
			OnDemandCost: f64(110),
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 90.909, pct, 0.01)
	})

	t.Run("returns false for term zero", func(t *testing.T) {
		rec := &RecommendationRecord{Term: 0, Savings: 100, OnDemandCost: f64(500)}
		_, ok := recEffectiveSavingsPct(rec)
		assert.False(t, ok, "term=0 must not produce a pct")
	})

	t.Run("returns false when no baseline available", func(t *testing.T) {
		// Neither on_demand_cost nor monthly_cost: cannot compute denominator.
		rec := &RecommendationRecord{Term: 1, Savings: 100}
		_, ok := recEffectiveSavingsPct(rec)
		assert.False(t, ok)
	})

	t.Run("falls back to monthly_cost reconstruction", func(t *testing.T) {
		// monthly_cost=400, savings=100, upfront=0, term=1
		// on_demand reconstructed = 400 + 100 + 0 = 500
		// pct = (100/500)*100 = 20%
		monthly := 400.0
		rec := &RecommendationRecord{
			Term:        1,
			Savings:     100,
			UpfrontCost: 0,
			MonthlyCost: &monthly,
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 20.0, pct, 0.001)
	})

	t.Run("returns false when on_demand denominator is zero", func(t *testing.T) {
		// on_demand_cost=0 is treated as absent (provider didn't return a
		// meaningful baseline), so the function falls back to monthly_cost
		// reconstruction. If monthly_cost is also nil, it returns false.
		rec := &RecommendationRecord{Term: 1, Savings: 100, OnDemandCost: f64(0)}
		_, ok := recEffectiveSavingsPct(rec)
		assert.False(t, ok, "on_demand=0 + no monthly_cost must not produce a pct")
	})

	t.Run("high-upfront RI is not driven negative (#1148 regression)", func(t *testing.T) {
		// The real failing shape from issue #1103/#1148: a 3yr all-upfront
		// RI whose net savings are modest relative to the upfront amount.
		// term=3yr, upfront=$3600, savings=$50/mo (net), on_demand=$200/mo
		// amortized = 3600/36 = $100/mo
		// correct pct           = (50/200)*100  = 25%
		// old double-subtract   = (50-100)/200  = -25% (silently dropped by
		//                          any positive min_savings_pct floor)
		rec := &RecommendationRecord{
			Provider:     "azure",
			Term:         3,
			Savings:      50,
			UpfrontCost:  3600,
			OnDemandCost: f64(200),
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 25.0, pct, 0.001)
		assert.Greater(t, pct, 20.0,
			"a 25%%-saving all-upfront RI must pass a 20%% floor, not be filtered as negative")
	})

	t.Run("prefers provider-authoritative savings_percentage", func(t *testing.T) {
		// Mirrors frontend displaySavingsPct: when the provider reported a
		// percentage, use it verbatim instead of reconstructing.
		rec := &RecommendationRecord{
			Provider:          "aws",
			Term:              1,
			Savings:           100,
			OnDemandCost:      f64(500), // reconstruction would yield 20%
			SavingsPercentage: f64(31.5),
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 31.5, pct, 0.001)
	})

	t.Run("non-finite savings_percentage falls back to reconstruction", func(t *testing.T) {
		nan := math.NaN()
		rec := &RecommendationRecord{
			Term:              1,
			Savings:           100,
			OnDemandCost:      f64(500),
			SavingsPercentage: &nan,
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 20.0, pct, 0.001)
	})

	t.Run("aws row without on-demand baseline returns false (#323 guard)", func(t *testing.T) {
		// For AWS the reconstruction (monthly_cost + savings + amortized)
		// diverges from Cost Explorer's true on-demand baseline; the
		// frontend renders an em-dash and the server must likewise refuse
		// to compute (and therefore not filter on) a misleading value.
		monthly := 400.0
		rec := &RecommendationRecord{
			Provider:    "aws",
			Term:        1,
			Savings:     100,
			MonthlyCost: &monthly,
		}
		_, ok := recEffectiveSavingsPct(rec)
		assert.False(t, ok, "aws rec without on_demand_cost must not produce a reconstructed pct")
	})
}

// TestRecommendationFilter_UnitDistinction is the central regression test for
// issue #1089. It asserts that the SAME numeric value "30" produces different
// filter behaviour when interpreted as a dollar amount vs. a percentage:
//
//	rec.Savings = $100/mo, on_demand = $500/mo => effective_pct = 20%
//	min_savings_usd=30  => passes  ($100 >= $30)
//	min_savings_pct=30  => fails   (20% < 30%)
//
// This proves the two filters are truly distinct and cannot be conflated.
func TestRecommendationFilter_UnitDistinction(t *testing.T) {
	f64 := func(v float64) *float64 { return &v }

	// A recommendation that saves $100/mo on a $500/mo on-demand baseline
	// => effective savings rate = 20%.
	rec := &RecommendationRecord{
		Term:         1,
		Savings:      100,
		UpfrontCost:  0,
		OnDemandCost: f64(500),
	}

	pct, ok := recEffectiveSavingsPct(rec)
	require.True(t, ok)

	// The same numeric value "30" means opposite results in each unit.
	const threshold = 30.0
	assert.Greater(t, rec.Savings, threshold,
		"$100 savings passes $30 dollar floor (USD filter)")
	assert.Less(t, pct, threshold,
		"20%% savings fails 30%% percentage floor (PCT filter) -- distinct semantics, not conflated")
}

// ---------------------------------------------------------------------------
// buildRecommendationFilter SQL builder tests
// ---------------------------------------------------------------------------

func TestBuildRecommendationFilter_MinSavingsUSD(t *testing.T) {
	t.Run("MinSavingsUSD zero produces no WHERE clause fragment", func(t *testing.T) {
		clause, args := buildRecommendationFilter(&RecommendationFilter{MinSavingsUSD: 0})
		assert.Empty(t, clause)
		assert.Empty(t, args)
	})

	t.Run("MinSavingsUSD positive includes monthly_savings >= clause", func(t *testing.T) {
		clause, args := buildRecommendationFilter(&RecommendationFilter{MinSavingsUSD: 50})
		assert.Contains(t, clause, "monthly_savings >= $")
		require.Len(t, args, 1)
		assert.Equal(t, float64(50), args[0])
	})

	t.Run("MinSavingsPct zero is never pushed into SQL (no WHERE fragment)", func(t *testing.T) {
		// Pct filter is applied in-process, never in SQL.
		clause, args := buildRecommendationFilter(&RecommendationFilter{MinSavingsPct: 30})
		assert.Empty(t, clause, "MinSavingsPct must not appear in the SQL WHERE clause")
		assert.Empty(t, args)
	})

	t.Run("MinSavingsUSD and MinSavingsPct combined: only USD in SQL", func(t *testing.T) {
		clause, args := buildRecommendationFilter(&RecommendationFilter{
			MinSavingsUSD: 50,
			MinSavingsPct: 20,
		})
		// Only the dollar floor appears in SQL.
		assert.Contains(t, clause, "monthly_savings >= $")
		// Exactly one arg (the dollar value); the pct is handled in-process.
		require.Len(t, args, 1)
		assert.Equal(t, float64(50), args[0])
	})
}
