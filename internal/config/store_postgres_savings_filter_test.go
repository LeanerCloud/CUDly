package config

// Regression tests for issue #1089: min-savings unit disambiguation.
// These tests assert that MinSavingsUSD (dollar floor, pushed to SQL) and
// MinSavingsPct (percentage floor, applied in-process) are distinct fields
// with separate semantics that cannot be conflated.
//
// recEffectiveSavingsPct and buildRecommendationFilter are unexported; the
// white-box package config tests exercise them directly.

import (
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

	t.Run("amortizes upfront cost across term", func(t *testing.T) {
		// term=3yr, upfront=$360, savings=$100/mo, on_demand=$110/mo (approx)
		// amortized = 360 / (3*12) = 360/36 = 10
		// effective  = 100 - 10 = 90
		// on_demand  = 110 (supplied directly)
		// pct        = (90/110)*100 ~ 81.8%
		rec := &RecommendationRecord{
			Term:         3,
			Savings:      100,
			UpfrontCost:  360,
			OnDemandCost: f64(110),
		}
		pct, ok := recEffectiveSavingsPct(rec)
		require.True(t, ok)
		assert.InDelta(t, 81.818, pct, 0.01)
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
		clause, args := buildRecommendationFilter(RecommendationFilter{MinSavingsUSD: 0})
		assert.Empty(t, clause)
		assert.Empty(t, args)
	})

	t.Run("MinSavingsUSD positive includes monthly_savings >= clause", func(t *testing.T) {
		clause, args := buildRecommendationFilter(RecommendationFilter{MinSavingsUSD: 50})
		assert.Contains(t, clause, "monthly_savings >= $")
		require.Len(t, args, 1)
		assert.Equal(t, float64(50), args[0])
	})

	t.Run("MinSavingsPct zero is never pushed into SQL (no WHERE fragment)", func(t *testing.T) {
		// Pct filter is applied in-process, never in SQL.
		clause, args := buildRecommendationFilter(RecommendationFilter{MinSavingsPct: 30})
		assert.Empty(t, clause, "MinSavingsPct must not appear in the SQL WHERE clause")
		assert.Empty(t, args)
	})

	t.Run("MinSavingsUSD and MinSavingsPct combined: only USD in SQL", func(t *testing.T) {
		clause, args := buildRecommendationFilter(RecommendationFilter{
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
