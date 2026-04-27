package exchange

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeReshaping_BaseEmitsNoAlternatives(t *testing.T) {
	t.Parallel()
	// The base AnalyzeReshaping no longer emits cross-family
	// alternatives — that responsibility now lives in
	// AnalyzeReshapingWithRecs, which sources options from the cached
	// AWS Cost Explorer recommendations table. The base path's
	// AlternativeTargets must always be nil regardless of family so the
	// auto.go pipeline (which uses the base path) keeps its current
	// "primary target only" contract.
	recs := AnalyzeReshaping(
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
	)
	require.Len(t, recs, 1)
	assert.Equal(t, "m5.large", recs[0].TargetInstanceType, "primary same-family downsize stays")
	assert.Nil(t, recs[0].AlternativeTargets,
		"base path emits no alternatives — recs-driven path owns that surface")
}

func TestAnalyzeReshaping_StandardRIStillSkipped(t *testing.T) {
	t.Parallel()
	// Regression guard: the refactor must not start emitting
	// recommendations for Standard RIs, which AWS forbids from
	// exchanging entirely.
	recs := AnalyzeReshaping(
		[]RIInfo{{ID: "ri-std", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "standard"}},
		[]UtilizationInfo{{RIID: "ri-std", UtilizationPercent: 30}},
		95,
	)
	assert.Empty(t, recs, "standard RI must never produce a recommendation")
}

// TestAnalyzeReshapingWithRecs_RecommendationDrivenAlternatives — the
// fake lookup returns offerings spanning m5/c5/r5; an underutilised m5
// RI should surface c5 + r5 alternatives (cross-family), with the same
// family (m5) excluded so the alternatives slice carries only options
// that differ meaningfully from the primary target. Sort order is
// ascending by EffectiveMonthlyCost — cheapest first.
func TestAnalyzeReshapingWithRecs_RecommendationDrivenAlternatives(t *testing.T) {
	t.Parallel()

	var callCount int
	var gotRegion, gotCurrency string
	lookup := func(_ context.Context, region, currency string) ([]OfferingOption, error) {
		callCount++
		gotRegion, gotCurrency = region, currency
		return []OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5", EffectiveMonthlyCost: 40.0, NormalizationFactor: 4, CurrencyCode: "USD"},
			{InstanceType: "c5.large", OfferingID: "off-c5", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4, CurrencyCode: "USD"},
			{InstanceType: "r5.large", OfferingID: "off-r5", EffectiveMonthlyCost: 60.0, NormalizationFactor: 4, CurrencyCode: "USD"},
		}, nil
	}

	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, CurrencyCode: "USD", // src units = 200
		}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	assert.Equal(t, 1, callCount, "lookup must be called exactly once per reshape pass")
	assert.Equal(t, "us-east-1", gotRegion, "region must be threaded through")
	assert.Equal(t, "USD", gotCurrency, "currency must be threaded through")

	got := recs[0]
	assert.Equal(t, "m5.large", got.TargetInstanceType, "primary downsize unchanged")

	gotAlts := make([]string, 0, len(got.AlternativeTargets))
	for _, alt := range got.AlternativeTargets {
		gotAlts = append(gotAlts, alt.InstanceType)
	}
	sort.Strings(gotAlts)
	assert.Equal(t, []string{"c5.large", "r5.large"}, gotAlts,
		"cross-family alternatives must surface; same-family m5 must be excluded")
	// Ascending by EffectiveMonthlyCost: c5 ($50) before r5 ($60).
	require.Len(t, got.AlternativeTargets, 2)
	assert.Equal(t, "c5.large", got.AlternativeTargets[0].InstanceType)
	assert.Equal(t, "off-c5", got.AlternativeTargets[0].OfferingID)
	assert.InDelta(t, 50.0, got.AlternativeTargets[0].EffectiveMonthlyCost, 0.001)
	assert.Equal(t, "r5.large", got.AlternativeTargets[1].InstanceType)
	assert.InDelta(t, 60.0, got.AlternativeTargets[1].EffectiveMonthlyCost, 0.001)
}

// TestAnalyzeReshapingWithRecs_EmptyLookupReturnsNoAlternatives — cold
// cache / no recs in the region: the rec ships with its primary target
// and an empty AlternativeTargets slice. UI matches "AWS hasn't
// recommended anything for this region yet".
func TestAnalyzeReshapingWithRecs_EmptyLookupReturnsNoAlternatives(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return nil, nil
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	assert.Equal(t, "m5.large", recs[0].TargetInstanceType)
	assert.Empty(t, recs[0].AlternativeTargets,
		"empty cache → no alternatives but primary target still surfaced")
}

// TestAnalyzeReshapingWithRecs_AppliesDollarUnitsFilter — alternatives
// that would fail AWS's $-units exchange check are dropped before
// reaching the UI, matching the existing behaviour of the local
// pre-filter. Source pricing must be supplied (NF + MonthlyCost) for
// the gate to engage.
func TestAnalyzeReshapingWithRecs_AppliesDollarUnitsFilter(t *testing.T) {
	t.Parallel()

	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			// c5.large priced too cheap to satisfy the units check
			// (4 × 5 = 20 < src 200) — should be filtered out.
			{InstanceType: "c5.large", OfferingID: "off-c5", EffectiveMonthlyCost: 5.0, NormalizationFactor: 4, CurrencyCode: "USD"},
			// r5.large priced enough to pass (4 × 50 = 200 ≥ 200).
			{InstanceType: "r5.large", OfferingID: "off-r5", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4, CurrencyCode: "USD"},
		}, nil
	}

	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, CurrencyCode: "USD", // src units = 200
		}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	alts := recs[0].AlternativeTargets
	require.Len(t, alts, 1, "only r5.large should pass; c5.large gets filtered for failing the $-units check")
	assert.Equal(t, "r5.large", alts[0].InstanceType)
}

// TestAnalyzeReshapingWithRecs_NoSourcePricingSkipsFilter — when the
// caller doesn't populate RIInfo.MonthlyCost (zero), the filter is
// skipped entirely and every cross-family offering passes through.
// Pins backwards compatibility for older callers that don't compute
// per-RI monthly cost.
func TestAnalyzeReshapingWithRecs_NoSourcePricingSkipsFilter(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			// Would normally be dropped if the filter ran, but no source
			// pricing means we keep today's "show every cross-family match"
			// behaviour.
			{InstanceType: "c5.large", OfferingID: "off-c5", EffectiveMonthlyCost: 5.0, NormalizationFactor: 4},
			{InstanceType: "r5.large", OfferingID: "off-r5", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
		}, nil
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		// Older RIInfo shape: no MonthlyCost / CurrencyCode populated.
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "",
		lookup,
	)
	require.Len(t, recs, 1)
	require.Len(t, recs[0].AlternativeTargets, 2,
		"with no source pricing, both cross-family alternatives must remain visible")
}

// TestAnalyzeReshapingWithRecs_ExcludesPrimaryTarget — when the lookup
// returns an offering whose InstanceType matches the rec's primary
// TargetInstanceType, that offering is excluded from AlternativeTargets
// because it isn't an alternative to itself; it's the same suggestion.
// The cross-family offerings that remain are surfaced as before.
func TestAnalyzeReshapingWithRecs_ExcludesPrimaryTarget(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			// Same family + same size as the primary target — must be
			// excluded by the same-family guard regardless.
			{InstanceType: "m5.large", OfferingID: "off-m5", EffectiveMonthlyCost: 40.0, NormalizationFactor: 4},
			{InstanceType: "c5.large", OfferingID: "off-c5", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
		}, nil
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "",
		lookup,
	)
	require.Len(t, recs, 1)
	require.Len(t, recs[0].AlternativeTargets, 1)
	assert.Equal(t, "c5.large", recs[0].AlternativeTargets[0].InstanceType)
}

// TestAnalyzeReshapingWithRecs_LookupErrorFallsBackToBaseRecs — Cost
// Explorer cache read fails: return the base recs (primary target
// intact, empty alternatives). Losing alternatives is strictly less
// bad than losing the whole reshape page.
func TestAnalyzeReshapingWithRecs_LookupErrorFallsBackToBaseRecs(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return nil, fmt.Errorf("postgres timeout")
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	assert.Equal(t, "m5.large", recs[0].TargetInstanceType)
	assert.Empty(t, recs[0].AlternativeTargets,
		"lookup error → no alternatives, primary target still ships")
}

// TestAnalyzeReshapingWithRecs_NilLookupUsesBaseRecs — defensive: nil
// lookup should not panic; the base recs flow through unchanged.
func TestAnalyzeReshapingWithRecs_NilLookupUsesBaseRecs(t *testing.T) {
	t.Parallel()
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		nil,
	)
	require.Len(t, recs, 1)
	assert.Equal(t, "m5.large", recs[0].TargetInstanceType)
	assert.Empty(t, recs[0].AlternativeTargets)
}

// TestAnalyzeReshapingWithRecs_LegacyFamilyM4GeneratesAlternatives —
// post-refactor, "legacy family" support is no longer hand-curated:
// any cross-family offering returned by the cached recs surfaces as
// long as it passes the dollar-units check. An underutilised m4 RI
// paired against c5 / r5 / m5 recs surfaces all three (m5 because it
// is a different family from m4 and the gate accepts).
func TestAnalyzeReshapingWithRecs_LegacyFamilyM4GeneratesAlternatives(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5l", EffectiveMonthlyCost: 60.0, NormalizationFactor: 4, CurrencyCode: "USD"},
			{InstanceType: "c5.large", OfferingID: "off-c5l", EffectiveMonthlyCost: 70.0, NormalizationFactor: 4, CurrencyCode: "USD"},
		}, nil
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-m4", InstanceType: "m4.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, CurrencyCode: "USD", // src units = 200
		}},
		[]UtilizationInfo{{RIID: "ri-m4", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	got := recs[0]
	assert.Equal(t, "m4.large", got.TargetInstanceType)
	require.Len(t, got.AlternativeTargets, 2,
		"both m5.large and c5.large should pass the $-units check")
	// Ascending by EffectiveMonthlyCost.
	assert.Equal(t, "m5.large", got.AlternativeTargets[0].InstanceType)
	assert.Equal(t, "c5.large", got.AlternativeTargets[1].InstanceType)
}

// TestAnalyzeReshapingWithRecs_TermMismatchedAlternativesFiltered pins
// the term-match guard: a 1y source RI must not see 3y alternatives in
// AlternativeTargets (and vice versa) because AWS only allows exchanges
// within the same term. Both sides populate TermSeconds — the guard
// rejects the mismatched offering before AlternativeTargets is built.
const oneYearSeconds = int64(365 * 24 * 60 * 60)
const threeYearSeconds = 3 * oneYearSeconds

func TestAnalyzeReshapingWithRecs_TermMismatchedAlternativesFiltered(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			// 1y c5.large — same term as the source, must surface.
			{InstanceType: "c5.large", OfferingID: "off-c5-1y", EffectiveMonthlyCost: 50, NormalizationFactor: 4, CurrencyCode: "USD", TermSeconds: oneYearSeconds},
			// 3y r5.large — term mismatch, must be filtered out even
			// though it would otherwise pass the $-units check.
			{InstanceType: "r5.large", OfferingID: "off-r5-3y", EffectiveMonthlyCost: 60, NormalizationFactor: 4, CurrencyCode: "USD", TermSeconds: threeYearSeconds},
		}, nil
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, CurrencyCode: "USD",
			TermSeconds: oneYearSeconds, // 1y source
		}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	require.Len(t, recs[0].AlternativeTargets, 1,
		"only the 1y alternative survives; the 3y r5.large must be filtered as term-mismatched")
	assert.Equal(t, "c5.large", recs[0].AlternativeTargets[0].InstanceType)
	assert.Equal(t, oneYearSeconds, recs[0].AlternativeTargets[0].TermSeconds,
		"surviving alternative must carry the source's term so the UI can label it")
}

// TestAnalyzeReshapingWithRecs_TermZeroSkipsTermGuard pins the
// backwards-compat path: when either the source or the offering omits
// TermSeconds, the guard does not fire and the alternative passes
// through (subject to the other gates). Mirrors the existing
// MonthlyCost==0 / CurrencyCode=="" skip semantics.
func TestAnalyzeReshapingWithRecs_TermZeroSkipsTermGuard(t *testing.T) {
	t.Parallel()
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			// 3y offering, but the source has no term info — the term
			// gate stays disabled so the offering surfaces.
			{InstanceType: "r5.large", OfferingID: "off-r5-3y", EffectiveMonthlyCost: 60, NormalizationFactor: 4, CurrencyCode: "USD", TermSeconds: threeYearSeconds},
		}, nil
	}
	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, CurrencyCode: "USD",
			// TermSeconds intentionally zero — older callers / fixtures.
		}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	require.Len(t, recs[0].AlternativeTargets, 1,
		"source TermSeconds==0 must skip the term gate so today's behaviour is preserved")
	assert.Equal(t, "r5.large", recs[0].AlternativeTargets[0].InstanceType)
}

// TestPassesDollarUnitsCheck pins the local pre-filter rule that gates
// cross-family alternatives. The rule is conservative: when source NF
// or any side's price is zero, the check returns false (skip). When
// currencies differ explicitly, it returns false. Otherwise the
// (NF × EMC) product comparison decides.
func TestPassesDollarUnitsCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		srcNF       float64
		srcMC       float64
		srcCurrency string
		target      OfferingOption
		want        bool
	}{
		{
			name:  "source < target by NF×EMC: allow",
			srcNF: 8, srcMC: 100, // src units = 800
			target: OfferingOption{NormalizationFactor: 16, EffectiveMonthlyCost: 60}, // tgt units = 960
			want:   true,
		},
		{
			name:  "source == target by NF×EMC: allow (boundary)",
			srcNF: 8, srcMC: 100, // src units = 800
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 100}, // tgt units = 800
			want:   true,
		},
		{
			name:  "source > target by NF×EMC: deny",
			srcNF: 8, srcMC: 100, // src units = 800
			target: OfferingOption{NormalizationFactor: 4, EffectiveMonthlyCost: 50}, // tgt units = 200
			want:   false,
		},
		{
			name:  "zero source NF: deny",
			srcNF: 0, srcMC: 100,
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 100},
			want:   false,
		},
		{
			name:  "zero target EMC: deny (defensive)",
			srcNF: 8, srcMC: 100,
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 0},
			want:   false,
		},
		{
			name:  "zero target NF: deny (defensive)",
			srcNF: 8, srcMC: 100,
			target: OfferingOption{NormalizationFactor: 0, EffectiveMonthlyCost: 100},
			want:   false,
		},
		{
			name:  "explicit currency mismatch: deny",
			srcNF: 8, srcMC: 100, srcCurrency: "USD",
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 100, CurrencyCode: "EUR"},
			want:   false,
		},
		{
			name:  "matching currencies: allow",
			srcNF: 8, srcMC: 100, srcCurrency: "USD",
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 100, CurrencyCode: "USD"},
			want:   true,
		},
		{
			name:  "empty source currency: skip currency guard",
			srcNF: 8, srcMC: 100, srcCurrency: "",
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 100, CurrencyCode: "EUR"},
			want:   true,
		},
		{
			name:  "empty target currency: skip currency guard",
			srcNF: 8, srcMC: 100, srcCurrency: "USD",
			target: OfferingOption{NormalizationFactor: 8, EffectiveMonthlyCost: 100, CurrencyCode: ""},
			want:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := passesDollarUnitsCheck(c.srcNF, c.srcMC, c.srcCurrency, c.target)
			assert.Equal(t, c.want, got)
		})
	}
}
