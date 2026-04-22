package exchange

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCandidateFamilies_AllowlistedFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source string
		want   []string
	}{
		// General-purpose peers.
		{"m5", []string{"m5", "m6i", "m7g"}},
		{"m6i", []string{"m5", "m6i", "m7g"}},
		{"m7g", []string{"m5", "m6i", "m7g"}},
		// Compute-optimised peers.
		{"c5", []string{"c5", "c6i", "c7g"}},
		{"c7g", []string{"c5", "c6i", "c7g"}},
		// Memory-optimised peers.
		{"r5", []string{"r5", "r6i", "r7g"}},
		{"r6i", []string{"r5", "r6i", "r7g"}},
		// Burstable peers.
		{"t3", []string{"t3", "t3a", "t4g"}},
		// Case insensitivity — operators may supply uppercase.
		{"M5", []string{"m5", "m6i", "m7g"}},
	}
	for _, c := range cases {
		got := candidateFamilies(c.source)
		sort.Strings(got)
		sort.Strings(c.want)
		assert.Equal(t, c.want, got, "unexpected peers for %q", c.source)
	}
}

func TestCandidateFamilies_SpecialtyAndLegacyFamilies(t *testing.T) {
	t.Parallel()
	// Specialty (GPU/HPC) and legacy-generation families are now in
	// the allowlist; the local passesDollarUnitsCheck pre-filter (in
	// fillAlternativesFromOfferings) drops unviable cross-family
	// pairs so suggestions stay actionable without per-pair AWS quote
	// API calls. These groups are fixed in code; the test pins the
	// exact membership.
	cases := []struct {
		source string
		want   []string
	}{
		// GPU
		{"p3", []string{"p3", "p4d", "p5"}},
		{"p4d", []string{"p3", "p4d", "p5"}},
		{"g4dn", []string{"g4dn", "g5"}},
		// HPC
		{"hpc6a", []string{"hpc6a", "hpc6id", "hpc7g"}},
		// Legacy generations.
		{"m4", []string{"m4", "m5"}},
		{"c4", []string{"c4", "c5"}},
		{"r3", []string{"r3", "r4", "r5"}},
		{"r4", []string{"r3", "r4", "r5"}},
	}
	for _, c := range cases {
		got := candidateFamilies(c.source)
		sort.Strings(got)
		sort.Strings(c.want)
		assert.Equal(t, c.want, got, "unexpected peers for %q", c.source)
	}
}

func TestCandidateFamilies_UnlistedFamiliesReturnNil(t *testing.T) {
	t.Parallel()
	// Truly off-allowlist families: x1 / g4 (note: g4dn IS in the
	// allowlist, but plain "g4" without the dn suffix isn't), older
	// HPC variants, and the empty string. These return nil — no
	// cross-family suggestions surface for the underlying RI.
	for _, fam := range []string{"g4", "x1", "hpc7a", ""} {
		assert.Nil(t, candidateFamilies(fam), "expected nil for unlisted family %q", fam)
	}
}

func TestAnalyzeReshaping_EmitsCrossFamilyAlternatives(t *testing.T) {
	t.Parallel()
	// m5.xlarge at 50% → primary target m5.large; alternatives
	// should list m6i.large and m7g.large (same peer group, same
	// target size, source family excluded).
	recs := AnalyzeReshaping(
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
	)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation; got %d", len(recs))
	}
	got := recs[0]
	assert.Equal(t, "m5.large", got.TargetInstanceType)

	gotAlts := make([]string, 0, len(got.AlternativeTargets))
	for _, alt := range got.AlternativeTargets {
		gotAlts = append(gotAlts, alt.InstanceType)
	}
	sort.Strings(gotAlts)
	assert.Equal(t, []string{"m6i.large", "m7g.large"}, gotAlts,
		"peer families should be suggested at the same target size, source family excluded")
	// No lookup injected at this call site → pricing fields stay zero.
	for _, alt := range got.AlternativeTargets {
		assert.Empty(t, alt.OfferingID, "base AnalyzeReshaping does not resolve offering IDs")
		assert.Zero(t, alt.EffectiveMonthlyCost, "base AnalyzeReshaping does not resolve prices")
	}
}

func TestAnalyzeReshaping_NoAlternativesForUnlistedFamily(t *testing.T) {
	t.Parallel()
	// "x1" is not in the allowlist, so no cross-family alternatives
	// are emitted at the base layer. We still emit the same-family
	// primary reshape — name-only alternatives only get filled in
	// when the family has peers in the group.
	recs := AnalyzeReshaping(
		[]RIInfo{{ID: "ri-x", InstanceType: "x1.16xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-x", UtilizationPercent: 40}},
		95,
	)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation; got %d", len(recs))
	}
	assert.Nil(t, recs[0].AlternativeTargets,
		"unlisted families must not get cross-family alternatives")
}

func TestAnalyzeReshaping_StandardRIStillSkipped(t *testing.T) {
	t.Parallel()
	// Regression guard: adding cross-family suggestions must not
	// start emitting recommendations for Standard RIs, which AWS
	// forbids from exchanging entirely.
	recs := AnalyzeReshaping(
		[]RIInfo{{ID: "ri-std", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "standard"}},
		[]UtilizationInfo{{RIID: "ri-std", UtilizationPercent: 30}},
		95,
	)
	assert.Empty(t, recs, "standard RI must never produce a recommendation")
}

func TestAnalyzeReshapingWithOfferings_EnrichesAlternatives(t *testing.T) {
	t.Parallel()
	// m5.xlarge at 50% → primary m5.large + alternatives m6i.large,
	// m7g.large. Lookup returns pricing for all three plus an extra
	// unrelated offering that should be ignored. Lookup is called
	// exactly once with the de-duplicated instance-type set.
	var gotTypes []string
	var callCount int
	lookup := func(ctx context.Context, instanceTypes []string) ([]OfferingOption, error) {
		callCount++
		gotTypes = append([]string{}, instanceTypes...)
		return []OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5", EffectiveMonthlyCost: 40.0},
			{InstanceType: "m6i.large", OfferingID: "off-m6i", EffectiveMonthlyCost: 35.0},
			{InstanceType: "m7g.large", OfferingID: "off-m7g", EffectiveMonthlyCost: 30.0},
			{InstanceType: "irrelevant.size", OfferingID: "off-x", EffectiveMonthlyCost: 999.0},
		}, nil
	}

	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		lookup,
	)
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec; got %d", len(recs))
	}
	assert.Equal(t, 1, callCount, "lookup must be called exactly once (batched)")

	sort.Strings(gotTypes)
	assert.Equal(t, []string{"m5.large", "m6i.large", "m7g.large"}, gotTypes,
		"lookup should receive de-duplicated instance types")

	got := recs[0]
	assert.Equal(t, "m5.large", got.TargetInstanceType)
	if len(got.AlternativeTargets) != 2 {
		t.Fatalf("expected 2 alternatives; got %+v", got.AlternativeTargets)
	}
	// AnalyzeReshapingWithOfferings sorts alternatives ascending by
	// EffectiveMonthlyCost so the cheapest option is first. Lookup
	// returned m6i.large=$35 and m7g.large=$30 → m7g first.
	assert.Equal(t, "m7g.large", got.AlternativeTargets[0].InstanceType)
	assert.Equal(t, "off-m7g", got.AlternativeTargets[0].OfferingID)
	assert.InDelta(t, 30.0, got.AlternativeTargets[0].EffectiveMonthlyCost, 0.001)
	assert.Equal(t, "m6i.large", got.AlternativeTargets[1].InstanceType)
	assert.InDelta(t, 35.0, got.AlternativeTargets[1].EffectiveMonthlyCost, 0.001)
}

func TestAnalyzeReshapingWithOfferings_MissingOfferingDroppedNotWholeRec(t *testing.T) {
	t.Parallel()
	// AWS doesn't offer m7g.large in this region. The lookup returns
	// only m5.large + m6i.large. The rec should still ship with
	// m5.large as primary and m6i.large as the only alternative;
	// m7g.large is silently dropped.
	lookup := func(ctx context.Context, _ []string) ([]OfferingOption, error) {
		return []OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5", EffectiveMonthlyCost: 40.0},
			{InstanceType: "m6i.large", OfferingID: "off-m6i", EffectiveMonthlyCost: 35.0},
		}, nil
	}
	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		lookup,
	)
	if len(recs) != 1 {
		t.Fatalf("expected rec to still ship; got %d", len(recs))
	}
	if len(recs[0].AlternativeTargets) != 1 || recs[0].AlternativeTargets[0].InstanceType != "m6i.large" {
		t.Fatalf("expected only m6i.large to survive; got %+v", recs[0].AlternativeTargets)
	}
}

func TestAnalyzeReshapingWithOfferings_LookupErrorFallsBackToBaseRecs(t *testing.T) {
	t.Parallel()
	// Cost Explorer 5xx → return base recs with empty
	// AlternativeTargets (primary target still shown). Losing pricing
	// is strictly less bad than losing the whole reshape page.
	lookup := func(ctx context.Context, _ []string) ([]OfferingOption, error) {
		return nil, fmt.Errorf("api call failed")
	}
	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		lookup,
	)
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec; got %d", len(recs))
	}
	assert.Equal(t, "m5.large", recs[0].TargetInstanceType)
	// Base recs had name-only alternatives; lookup failed so those
	// are preserved as-is (not silently cleared). The handler can
	// still render instance-type chips even without pricing.
	assert.NotEmpty(t, recs[0].AlternativeTargets,
		"lookup error should leave the name-only alternatives intact")
}

func TestAnalyzeReshapingWithOfferings_NilLookupUsesBaseRecs(t *testing.T) {
	t.Parallel()
	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		[]RIInfo{{ID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-1", UtilizationPercent: 50}},
		95,
		nil,
	)
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec; got %d", len(recs))
	}
	assert.NotEmpty(t, recs[0].AlternativeTargets,
		"nil lookup should still leave the name-only alternatives in place")
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

// TestAnalyzeReshapingWithOfferings_LegacyFamilyM4GeneratesM5Alternative
// — given an underutilised m4 RI, the new legacy-family entry in
// peerFamilyGroups + the dollar-units pre-filter should surface m5 as
// an alternative when its (NF × EMC) is at least the source's units.
func TestAnalyzeReshapingWithOfferings_LegacyFamilyM4GeneratesM5Alternative(t *testing.T) {
	t.Parallel()

	lookup := func(_ context.Context, _ []string) ([]OfferingOption, error) {
		return []OfferingOption{
			// m4.large primary: NF=4, EMC=$50 → 200 units (matches src 200, passes the boundary).
			{InstanceType: "m4.large", OfferingID: "off-m4l", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
			// m5.large alternative: NF=4, EMC=$60 → 240 units (>= src 200 → passes).
			{InstanceType: "m5.large", OfferingID: "off-m5l", EffectiveMonthlyCost: 60.0, NormalizationFactor: 4},
		}, nil
	}

	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		[]RIInfo{{
			ID: "ri-m4", InstanceType: "m4.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, // src units = 200
		}},
		[]UtilizationInfo{{RIID: "ri-m4", UtilizationPercent: 50}},
		95,
		lookup,
	)
	require.Len(t, recs, 1)
	got := recs[0]
	assert.Equal(t, "m4.large", got.TargetInstanceType)
	require.Len(t, got.AlternativeTargets, 1, "m5.large should pass the $-units check")
	assert.Equal(t, "m5.large", got.AlternativeTargets[0].InstanceType)
}

// TestAnalyzeReshapingWithOfferings_DollarUnitsFilterDropsUnviable
// — when an alternative would fail AWS's $-units check, the local
// pre-filter excludes it from the rec's AlternativeTargets so the UI
// doesn't show a suggestion that would be rejected at exchange time.
func TestAnalyzeReshapingWithOfferings_DollarUnitsFilterDropsUnviable(t *testing.T) {
	t.Parallel()

	lookup := func(_ context.Context, _ []string) ([]OfferingOption, error) {
		return []OfferingOption{
			// Primary m5.large stays untouched (it's not an "alternative").
			{InstanceType: "m5.large", OfferingID: "off-m5l", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
			// m6i.large priced too cheap to satisfy the units check — should be filtered out.
			{InstanceType: "m6i.large", OfferingID: "off-m6il", EffectiveMonthlyCost: 5.0, NormalizationFactor: 4},
			// m7g.large priced enough to pass.
			{InstanceType: "m7g.large", OfferingID: "off-m7gl", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
		}, nil
	}

	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		[]RIInfo{{
			ID: "ri-m5", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible",
			NormalizationFactor: 8, MonthlyCost: 25, // src units = 200
		}},
		[]UtilizationInfo{{RIID: "ri-m5", UtilizationPercent: 50}},
		95,
		lookup,
	)
	require.Len(t, recs, 1)
	alts := recs[0].AlternativeTargets
	require.Len(t, alts, 1, "only m7g.large should pass; m6i.large gets filtered for failing the $-units check")
	assert.Equal(t, "m7g.large", alts[0].InstanceType)
}

// TestAnalyzeReshapingWithOfferings_NoSourcePricingSkipsFilter — when
// the caller doesn't populate RIInfo.MonthlyCost (zero), the filter is
// skipped entirely and behaviour matches today's "name + offering"
// shape. Pins backwards compatibility for older callers.
func TestAnalyzeReshapingWithOfferings_NoSourcePricingSkipsFilter(t *testing.T) {
	t.Parallel()

	lookup := func(_ context.Context, _ []string) ([]OfferingOption, error) {
		return []OfferingOption{
			{InstanceType: "m5.large", OfferingID: "off-m5l", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
			// Would normally be dropped if the filter ran, but no source
			// pricing means we keep today's "show every match" behaviour.
			{InstanceType: "m6i.large", OfferingID: "off-m6il", EffectiveMonthlyCost: 5.0, NormalizationFactor: 4},
			{InstanceType: "m7g.large", OfferingID: "off-m7gl", EffectiveMonthlyCost: 50.0, NormalizationFactor: 4},
		}, nil
	}

	recs := AnalyzeReshapingWithOfferings(
		context.Background(),
		// Older RIInfo shape: no MonthlyCost / CurrencyCode populated.
		[]RIInfo{{ID: "ri-m5", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-m5", UtilizationPercent: 50}},
		95,
		lookup,
	)
	require.Len(t, recs, 1)
	require.Len(t, recs[0].AlternativeTargets, 2, "with no source pricing, both alternatives must remain visible")
}
