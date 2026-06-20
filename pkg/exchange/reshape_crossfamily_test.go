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
// fake lookup returns offerings spanning m5/c5/r5; an underutilized m5
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
// reaching the UI, matching the existing behavior of the local
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
			// behavior.
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
// long as it passes the dollar-units check. An underutilized m4 RI
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
		"source TermSeconds==0 must skip the term gate so today's behavior is preserved")
	assert.Equal(t, "r5.large", recs[0].AlternativeTargets[0].InstanceType)
}

// TestPassesDollarUnitsCheck pins the local pre-filter rule that gates
// cross-family alternatives. The rule is conservative: when source NF
// or any side's price is zero, the check returns false (skip). When
// currencies differ explicitly, it returns false. Otherwise the
// (NF × EMC) product comparison decides.
func TestPassesDollarUnitsCheck(t *testing.T) {
	t.Parallel()

	cases := []struct { //nolint:govet // fieldalignment: test case struct; layout reflects logical parameter grouping
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

// --- composite score ranking tests ---

// floatPtr is a test helper that returns a pointer to a float64 value.
func floatPtr(v float64) *float64 { return &v }

// TestCompositeScore_SameGenOutranksTermMismatch asserts that a
// near-perfect alternative (same family prefix, exact NF, high confidence)
// outranks an alternative with identical savings but no family match and
// no confidence signal.
func TestCompositeScore_SameGenOutranksTermMismatch(t *testing.T) {
	t.Parallel()
	src := RIInfo{
		InstanceType:        "m5.xlarge",
		NormalizationFactor: 8,
		MonthlyCost:         80,
	}
	// m6i.xlarge: same "m" prefix (gen jump bonus), exact NF, high confidence.
	nearPerfect := OfferingOption{
		InstanceType:         "m6i.xlarge",
		EffectiveMonthlyCost: 85, // slightly more expensive
		NormalizationFactor:  8,
		SavingsAbs:           floatPtr(220),
		RecommendationCount:  5,
	}
	// r5.xlarge: different prefix (no family gen bonus), same NF, no confidence.
	crossFamily := OfferingOption{
		InstanceType:         "r5.xlarge",
		EffectiveMonthlyCost: 80, // same cost as source -- better raw price
		NormalizationFactor:  8,
	}
	scoreNearPerfect := compositeScore(nearPerfect, src)
	scoreCrossFamily := compositeScore(crossFamily, src)
	assert.Greater(t, scoreNearPerfect, scoreCrossFamily,
		"same-gen m6i should outrank cross-family r5 despite being slightly more expensive")
}

// TestCompositeScore_SameArchOutranksCrossArch asserts that a same-
// architecture alternative ranks higher than a cross-arch offering when
// all other signals (family prefix, cost, NF, confidence) are equal.
// Both offerings share the same "c" family prefix as the source so the
// family-gen component is identical and only the arch component differs.
func TestCompositeScore_SameArchOutranksCrossArch(t *testing.T) {
	t.Parallel()
	src := RIInfo{
		InstanceType:        "c5.xlarge", // Intel x86, prefix "c"
		NormalizationFactor: 8,
		MonthlyCost:         90,
	}
	// c6i: same "c" prefix (family gen bonus), Intel x86 (same arch).
	sameArch := OfferingOption{
		InstanceType:         "c6i.xlarge",
		EffectiveMonthlyCost: 90,
		NormalizationFactor:  8,
	}
	// c6g: same "c" prefix (family gen bonus), Graviton ARM (cross arch).
	crossArch := OfferingOption{
		InstanceType:         "c6g.xlarge",
		EffectiveMonthlyCost: 90,
		NormalizationFactor:  8,
	}
	assert.Greater(t, compositeScore(sameArch, src), compositeScore(crossArch, src),
		"x86->x86 should outrank x86->ARM when family-gen bonus is equal for both")
}

// TestCompositeScore_HighConfidenceOutranksLow asserts that a high-
// confidence recommendation (large savings + large fleet) ranks higher
// than a low-confidence one at otherwise equal signals.
func TestCompositeScore_HighConfidenceOutranksLow(t *testing.T) {
	t.Parallel()
	src := RIInfo{
		InstanceType:        "c5.xlarge",
		NormalizationFactor: 8,
		MonthlyCost:         70,
	}
	highConf := OfferingOption{
		InstanceType:         "r5.xlarge",
		EffectiveMonthlyCost: 70,
		NormalizationFactor:  8,
		SavingsAbs:           floatPtr(250),
		RecommendationCount:  4,
	}
	lowConf := OfferingOption{
		InstanceType:         "r5.xlarge",
		EffectiveMonthlyCost: 70,
		NormalizationFactor:  8,
		SavingsAbs:           floatPtr(10),
		RecommendationCount:  1,
	}
	assert.Greater(t, compositeScore(highConf, src), compositeScore(lowConf, src),
		"high-confidence CE rec should outrank low-confidence at equal cost")
}

// TestCompositeScore_AbsentSavingsIsNeutral asserts that an offering with
// nil SavingsAbs does not get penalized relative to a low-confidence one.
// Per the nullable-not-zero rule, absent data must not be coerced to 0.
func TestCompositeScore_AbsentSavingsIsNeutral(t *testing.T) {
	t.Parallel()
	src := RIInfo{
		InstanceType:        "c5.xlarge",
		NormalizationFactor: 8,
		MonthlyCost:         70,
	}
	absentSavings := OfferingOption{
		InstanceType:         "r6i.xlarge",
		EffectiveMonthlyCost: 70,
		NormalizationFactor:  8,
		SavingsAbs:           nil, // not supplied
	}
	lowConf := OfferingOption{
		InstanceType:         "r6i.xlarge",
		EffectiveMonthlyCost: 70,
		NormalizationFactor:  8,
		SavingsAbs:           floatPtr(5), // explicitly low confidence
	}
	// absent must score >= low confidence (neutral 0.5 vs. 0.0)
	assert.GreaterOrEqual(t, compositeScore(absentSavings, src), compositeScore(lowConf, src),
		"nil SavingsAbs should be treated as neutral (0.5), not as low confidence (0.0)")
}

// TestCompositeScore_FamilyPrefixDetection confirms familyPrefix strips
// generation digits and suffixes correctly for both x86 and ARM families.
func TestCompositeScore_FamilyPrefixDetection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		family string
		want   string
	}{
		{"m5", "m"},
		{"m6i", "m"},
		{"m6g", "m"},
		{"c6gn", "c"},
		{"r5", "r"},
		{"hpc7g", "hpc"},
		{"t4g", "t"},
	}
	for _, c := range cases {
		t.Run(c.family, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, familyPrefix(c.family))
		})
	}
}

// TestCompositeScore_ARMDetection confirms isARMFamily correctly classifies
// Graviton (ARM) vs. non-Graviton (x86/AMD) families.
func TestCompositeScore_ARMDetection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		family string
		arm    bool
	}{
		// Graviton (ARM) families.
		{"m6g", true},
		{"m6gd", true},
		{"m6gn", true},
		{"m7g", true},
		{"hpc7g", true},
		{"t4g", true},
		// x86 / AMD families -- must NOT be classified as ARM.
		{"m6i", false},
		{"c5n", false},
		{"r6a", false}, // AMD, not ARM
		{"m5", false},
		{"r5d", false},
		// Intel Dense Storage/Network families whose names contain "g"
		// but are x86-only and were previously false-positives.
		{"is4gen", false}, // Intel Dense Storage Gen 4, x86
		{"im4gn", false},  // Intel Dense NVMe, x86
		// NVIDIA GPU families (x86) whose name starts with "g".
		{"g4dn", false},
		{"g5", false},
	}
	for _, c := range cases {
		t.Run(c.family, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.arm, isARMFamily(c.family),
				"isARMFamily(%q) should be %v", c.family, c.arm)
		})
	}
}

// TestFillAlternativesFromRecs_DeterministicOrder verifies that two offerings
// with identical composite scores AND identical EffectiveMonthlyCost are
// always ordered by InstanceType lexicographically, ensuring a stable,
// reproducible result regardless of the input slice order.
func TestFillAlternativesFromRecs_DeterministicOrder(t *testing.T) {
	t.Parallel()

	// Both offerings are x86, same-prefix "m" (-> family gen bonus = 1),
	// same EMC, same savings -> identical composite scores. Without the
	// InstanceType tie-break the output order would be non-deterministic.
	savings := 60.0
	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		// Deliberately provide offerings in reverse-lexical order so that
		// the test fails if the tie-break is absent.
		return []OfferingOption{
			{
				InstanceType: "m6i.xlarge", OfferingID: "off-m6i",
				EffectiveMonthlyCost: 10, NormalizationFactor: 8, CurrencyCode: "USD",
				SavingsAbs: &savings, RecommendationCount: 2,
			},
			{
				InstanceType: "m6a.xlarge", OfferingID: "off-m6a",
				EffectiveMonthlyCost: 10, NormalizationFactor: 8, CurrencyCode: "USD",
				SavingsAbs: &savings, RecommendationCount: 2,
			},
		}, nil
	}

	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 10, CurrencyCode: "USD",
		}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	alts := recs[0].AlternativeTargets
	require.Len(t, alts, 2, "both cross-family alternatives must survive eligibility gates")

	// Lexicographic order: "m6a.xlarge" < "m6i.xlarge"
	assert.Equal(t, "m6a.xlarge", alts[0].InstanceType,
		"equal-score equal-EMC alternatives must be ordered lexicographically by InstanceType")
	assert.Equal(t, "m6i.xlarge", alts[1].InstanceType,
		"equal-score equal-EMC alternatives must be ordered lexicographically by InstanceType")
}

// TestAnalyzeReshapingWithRecs_CompositeScoreOrdersAlternatives is a
// snapshot-style integration test: given three alternatives with distinct
// composite score profiles, the slice must arrive in composite-score
// (descending) order.
//
// Source: m5.xlarge, NF=8, MonthlyCost=10 (src_units = 80).
// All alternatives use NF=8 and EMC=10 (units = 80 >= 80, passes the
// dollar-units gate). Their composite ranking is determined entirely by
// the non-cost signals:
//
//   - m6i.xlarge: same "m" prefix (family gen bonus=1), x86->x86 (arch=1), high confidence
//   - m6g.xlarge: same "m" prefix (family gen bonus=1), x86->ARM (arch=0), medium confidence
//   - c5.xlarge:  cross prefix (family gen=0), x86->x86 (arch=1), low confidence
//
// Expected order: m6i > m6g > c5.
func TestAnalyzeReshapingWithRecs_CompositeScoreOrdersAlternatives(t *testing.T) {
	t.Parallel()

	highSavings := 220.0
	medSavings := 60.0
	lowSavings := 15.0

	lookup := func(_ context.Context, _, _ string) ([]OfferingOption, error) {
		return []OfferingOption{
			{
				InstanceType: "c5.xlarge", OfferingID: "off-c5",
				EffectiveMonthlyCost: 10, NormalizationFactor: 8, CurrencyCode: "USD",
				SavingsAbs: &lowSavings, RecommendationCount: 1,
			},
			{
				InstanceType: "m6g.xlarge", OfferingID: "off-m6g",
				EffectiveMonthlyCost: 10, NormalizationFactor: 8, CurrencyCode: "USD",
				SavingsAbs: &medSavings, RecommendationCount: 2,
			},
			{
				InstanceType: "m6i.xlarge", OfferingID: "off-m6i",
				EffectiveMonthlyCost: 10, NormalizationFactor: 8, CurrencyCode: "USD",
				SavingsAbs: &highSavings, RecommendationCount: 4,
			},
		}, nil
	}

	recs := AnalyzeReshapingWithRecs(
		context.Background(),
		[]RIInfo{{
			ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 10, CurrencyCode: "USD",
		}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		"us-east-1", "USD",
		lookup,
	)
	require.Len(t, recs, 1)
	alts := recs[0].AlternativeTargets
	require.Len(t, alts, 3, "all three cross-family alternatives must survive the eligibility gates")

	// Assert composite-score ordering: m6i > m6g > c5.
	assert.Equal(t, "m6i.xlarge", alts[0].InstanceType, "m6i: same prefix + high confidence should rank first")
	assert.Equal(t, "m6g.xlarge", alts[1].InstanceType, "m6g: same prefix but ARM penalty places it second")
	assert.Equal(t, "c5.xlarge", alts[2].InstanceType, "c5: no family bonus + low confidence should rank last")
}
