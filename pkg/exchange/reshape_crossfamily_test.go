package exchange

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestCandidateFamilies_UnlistedFamiliesReturnNil(t *testing.T) {
	t.Parallel()
	// Specialty families (GPU, HPC) and legacy generations are
	// deliberately NOT in the allowlist — cross-family exchanges for
	// these tend to fail AWS's $-units check at quote time, so
	// surfacing them to users would hurt trust.
	for _, fam := range []string{"p3", "g4", "x1", "hpc7a", "m4", "c4", "r3", ""} {
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
	// p3.2xlarge is a specialty family, not in the allowlist.
	// We still emit a same-family primary reshape, but with no
	// alternatives to avoid pushing users toward exchanges that AWS
	// is likely to reject at quote time.
	recs := AnalyzeReshaping(
		[]RIInfo{{ID: "ri-gpu", InstanceType: "p3.2xlarge", InstanceCount: 1, OfferingClass: "convertible"}},
		[]UtilizationInfo{{RIID: "ri-gpu", UtilizationPercent: 40}},
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
