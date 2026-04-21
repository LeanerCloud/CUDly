package exchange

import (
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

	gotAlts := append([]string{}, got.AlternativeTargetInstanceTypes...)
	sort.Strings(gotAlts)
	assert.Equal(t, []string{"m6i.large", "m7g.large"}, gotAlts,
		"peer families should be suggested at the same target size, source family excluded")
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
	assert.Nil(t, recs[0].AlternativeTargetInstanceTypes,
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
