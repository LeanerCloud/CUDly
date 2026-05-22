package reservations

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already conformant",
			input: "VM_Reservation_Standard_D2a_v4",
			want:  "VM_Reservation_Standard_D2a_v4",
		},
		{
			name:  "spaces replaced by underscore",
			input: "VM Reservation Standard D2s v3",
			want:  "VM_Reservation_Standard_D2s_v3",
		},
		{
			name:  "special chars replaced",
			input: "Redis@Cache#Reservation!foo",
			want:  "Redis_Cache_Reservation_foo",
		},
		{
			name:  "runs of non-conforming chars collapsed to single underscore",
			input: "foo  bar!!baz",
			want:  "foo_bar_baz",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "exact 64 chars unchanged",
			input: strings.Repeat("a", 64),
			want:  strings.Repeat("a", 64),
		},
		{
			name:  "65 chars truncated to 64",
			input: strings.Repeat("b", 65),
			want:  strings.Repeat("b", 64),
		},
		{
			name:  "100 chars truncated to 64",
			input: strings.Repeat("c", 100),
			want:  strings.Repeat("c", 64),
		},
		{
			name:  "hyphens preserved",
			input: "Standard-D2a-v4",
			want:  "Standard-D2a-v4",
		},
		{
			name:  "mixed case preserved",
			input: "Redis_Cache_Reservation_Standard_D2s_v3",
			want:  "Redis_Cache_Reservation_Standard_D2s_v3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeDisplayName(tc.input)
			assert.Equal(t, tc.want, got)
			// Output must always match the Azure allowlist.
			if got != "" {
				assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
			}
		})
	}
}

// fixedTime is a constant timestamp used across BuildDisplayName tests for
// deterministic comparison. 2026-05-22 19:00:00 UTC.
var fixedTime = time.Date(2026, 5, 22, 19, 0, 0, 0, time.UTC)

// fixedRand is a 4-byte slice yielding the hex suffix "a1b2c3d4".
var fixedRand = []byte{0xa1, 0xb2, 0xc3, 0xd4}

func TestBuildDisplayName_HappyPath(t *testing.T) {
	got := BuildDisplayName(DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard_D2a_v4",
		Count:        1,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	want := "vm-eastus-Standard_D2a_v4-1x-1yr-allup-20260522T190000-a1b2c3d4"
	assert.Equal(t, want, got)
	assert.LessOrEqual(t, len(got), 64)
	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
}

func TestBuildDisplayName_PerServiceExamples(t *testing.T) {
	// One realistic example per service — the call sites use these
	// service codes. Test catches accidental swaps in the literal strings.
	cases := []struct {
		svc      string
		region   string
		sku      string
		wantHead string
	}{
		{"vm", "eastus", "Standard_D2a_v4", "vm-eastus-Standard_D2a_v4-"},
		{"redis", "westeurope", "Premium_P1", "redis-westeurope-Premium_P1-"},
		{"cosmos", "northeurope", "EnableCassandra", "cosmos-northeurope-EnableCassandra-"},
		{"sql", "centralus", "GP_Gen5_2", "sql-centralus-GP_Gen5_2-"},
		{"search", "westus2", "standard2", "search-westus2-standard2-"},
	}
	for _, tc := range cases {
		t.Run(tc.svc, func(t *testing.T) {
			got := BuildDisplayName(DisplayNameFields{
				Service:      tc.svc,
				Region:       tc.region,
				ResourceType: tc.sku,
				Count:        1,
				Term:         "1yr",
				Payment:      "all-upfront",
				Now:          fixedTime,
			}.WithRandSource(fixedRand))
			assert.True(t, strings.HasPrefix(got, tc.wantHead),
				"want prefix %q, got %q", tc.wantHead, got)
			assert.LessOrEqual(t, len(got), 64)
			assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
		})
	}
}

func TestBuildDisplayName_LengthFitDropsRandomFirst(t *testing.T) {
	// Long but realistic input: huge SKU + long region. Full format is
	// ~75 chars; builder must drop the random suffix first, then the
	// timestamp, keeping payment + the required segments.
	got := BuildDisplayName(DisplayNameFields{
		Service:      "search",
		Region:       "australiaeast",
		ResourceType: "Standard_NV24ads_A10_v5",
		Count:        999,
		Term:         "1yr",
		Payment:      "allup",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	assert.LessOrEqual(t, len(got), 64)
	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
	// All required segments must survive truncation.
	for _, must := range []string{"search", "australiaeast", "Standard_NV24ads_A10_v5", "999x", "1yr"} {
		assert.Contains(t, got, must, "required segment %q must survive truncation", must)
	}
	// Random suffix must be the first to go.
	assert.NotContains(t, got, "a1b2c3d4", "random suffix should be dropped to fit length cap")
}

func TestBuildDisplayName_LengthFitDropsTimestampNext(t *testing.T) {
	// Push beyond just dropping random — also need to drop timestamp.
	// Use a long SKU that pushes the total above 64 even without random.
	got := BuildDisplayName(DisplayNameFields{
		Service:      "search",
		Region:       "germanywestcentral", // 18 chars
		ResourceType: "Standard_NV24ads_A10_v5",
		Count:        999,
		Term:         "1yr",
		Payment:      "allup",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	assert.LessOrEqual(t, len(got), 64)
	for _, must := range []string{"search", "germanywestcentral", "Standard_NV24ads_A10_v5", "999x", "1yr"} {
		assert.Contains(t, got, must)
	}
	assert.NotContains(t, got, "a1b2c3d4")
	assert.NotContains(t, got, "20260522T190000")
	// Payment must still survive (drops after timestamp).
	assert.Contains(t, got, "allup")
}

func TestBuildDisplayName_LengthFitDropsPaymentLast(t *testing.T) {
	// Push beyond ts+random drops so paymt must also go, but keep total
	// short enough that all required segments still survive.
	// Sizes: "search"(6) + "germanywestcentral"(18) + SKU(25) + "9999x"(5)
	// + "1yr"(3) + 4 separators = 61 -- the longest combo where required
	// segments still fit and all optional ones must drop.
	got := BuildDisplayName(DisplayNameFields{
		Service:      "search",
		Region:       "germanywestcentral",
		ResourceType: strings.Repeat("X", 25),
		Count:        9999,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	assert.LessOrEqual(t, len(got), 64)
	for _, must := range []string{"search", "germanywestcentral", strings.Repeat("X", 25), "9999x", "1yr"} {
		assert.Contains(t, got, must)
	}
	// All optional segments dropped.
	assert.NotContains(t, got, "a1b2c3d4")
	assert.NotContains(t, got, "20260522T190000")
	assert.NotContains(t, got, "allup")
}

func TestBuildDisplayName_LengthFitTruncatesRequiredAsLastResort(t *testing.T) {
	// Even the required segments alone exceed 64. Builder must still
	// produce a ≤64-char allowlist-conformant string rather than panicking
	// or returning a too-long value.
	got := BuildDisplayName(DisplayNameFields{
		Service:      "search",
		Region:       "germanywestcentral",
		ResourceType: strings.Repeat("X", 80),
		Count:        9999,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	assert.LessOrEqual(t, len(got), 64)
	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
}

func TestBuildDisplayName_PaymentNormalization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"all-upfront", "allup"},
		{"All-Upfront", "allup"},
		{"upfront", "allup"},
		{"no-upfront", "noup"},
		{"No-Upfront", "noup"},
		{"partial-upfront", "partup"},
		{"Partial-Upfront", "partup"},
		{"monthly", "monthly"},
		{"Monthly", "monthly"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizePayment(tc.in))
		})
	}
}

func TestBuildDisplayName_PaymentNormalizationVisibleInOutput(t *testing.T) {
	cases := []struct {
		paymt   string
		wantSeg string
	}{
		{"all-upfront", "allup"},
		{"no-upfront", "noup"},
		{"partial-upfront", "partup"},
		{"monthly", "monthly"},
	}
	for _, tc := range cases {
		t.Run(tc.paymt, func(t *testing.T) {
			got := BuildDisplayName(DisplayNameFields{
				Service:      "vm",
				Region:       "eastus",
				ResourceType: "Standard_D2a_v4",
				Count:        1,
				Term:         "1yr",
				Payment:      tc.paymt,
				Now:          fixedTime,
			}.WithRandSource(fixedRand))
			// Payment segment is bracketed by dashes in the output.
			assert.Contains(t, got, "-"+tc.wantSeg+"-",
				"payment %q should normalize to segment %q in %q", tc.paymt, tc.wantSeg, got)
		})
	}
}

func TestBuildDisplayName_TermNormalization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1yr", "1yr"},
		{"1", "1yr"},
		{"1y", "1yr"},
		{"P1Y", "1yr"},
		{"3yr", "3yr"},
		{"3", "3yr"},
		{"P3Y", "3yr"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeTerm(tc.in))
		})
	}
}

func TestBuildDisplayName_SanitizesDirtyInput(t *testing.T) {
	// Unexpected chars in any field must be sanitized to underscores.
	got := BuildDisplayName(DisplayNameFields{
		Service:      "v m", // space
		Region:       "east/us",
		ResourceType: "Standard@D2a v4",
		Count:        1,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
	// Should not contain spaces, slashes, or @.
	assert.NotContains(t, got, " ")
	assert.NotContains(t, got, "/")
	assert.NotContains(t, got, "@")
}

func TestBuildDisplayName_EmbeddedDashInSegmentBecomesUnderscore(t *testing.T) {
	// A SKU containing a dash would create ambiguity with the join
	// separator. The builder collapses internal dashes to underscores.
	got := BuildDisplayName(DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard-D2a-v4",
		Count:        1,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	// The SKU's dashes should be underscores in the output.
	assert.Contains(t, got, "Standard_D2a_v4")
	// And the segment boundaries remain dashes.
	assert.True(t, strings.HasPrefix(got, "vm-eastus-Standard_D2a_v4-"))
}

func TestBuildDisplayName_Deterministic(t *testing.T) {
	// Same fields + same Now + same randSource -> identical output.
	f := DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard_D2a_v4",
		Count:        2,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}.WithRandSource(fixedRand)
	first := BuildDisplayName(f)
	second := BuildDisplayName(f)
	assert.Equal(t, first, second)
}

func TestBuildDisplayName_DifferentRandsProduceDifferentOutputs(t *testing.T) {
	base := DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard_D2a_v4",
		Count:        2,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}
	a := BuildDisplayName(base.WithRandSource([]byte{0x01, 0x02, 0x03, 0x04}))
	b := BuildDisplayName(base.WithRandSource([]byte{0xff, 0xee, 0xdd, 0xcc}))
	assert.NotEqual(t, a, b)
	assert.Contains(t, a, "01020304")
	assert.Contains(t, b, "ffeeddcc")
}

func TestBuildDisplayName_ProductionUsesCryptoRand(t *testing.T) {
	// No randSource set: builder reads from crypto/rand. The two calls
	// should differ in their 8-hex suffix with extremely high probability
	// (2^-32 collision), and both should still conform to the allowlist.
	base := DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard_D2a_v4",
		Count:        1,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          fixedTime,
	}
	a := BuildDisplayName(base)
	b := BuildDisplayName(base)
	assert.NotEqual(t, a, b)
	allowlist := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	require.Regexp(t, allowlist, a)
	require.Regexp(t, allowlist, b)
}

func TestBuildDisplayName_EmptyPaymentSegmentIsSkipped(t *testing.T) {
	got := BuildDisplayName(DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard_D2a_v4",
		Count:        1,
		Term:         "1yr",
		Payment:      "", // no payment info
		Now:          fixedTime,
	}.WithRandSource(fixedRand))

	// Must not contain double-dash from the missing payment segment.
	assert.NotContains(t, got, "--")
	// Order is preserved: 1yr is directly followed by the timestamp.
	assert.Contains(t, got, "-1yr-20260522T190000-a1b2c3d4")
}

func TestBuildDisplayName_TimestampUTC(t *testing.T) {
	// Even if the caller passes a local-zone time, the builder must
	// normalize to UTC so identifiers are comparable across hosts.
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	local := time.Date(2026, 5, 22, 12, 0, 0, 0, loc) // 19:00 UTC
	got := BuildDisplayName(DisplayNameFields{
		Service:      "vm",
		Region:       "eastus",
		ResourceType: "Standard_D2a_v4",
		Count:        1,
		Term:         "1yr",
		Payment:      "all-upfront",
		Now:          local,
	}.WithRandSource(fixedRand))
	assert.Contains(t, got, "20260522T190000")
}
