package commitmentopts

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDurationToTerm(t *testing.T) {
	cases := []struct {
		name    string
		seconds int64
		term    int
		ok      bool
	}{
		{"1yr", 31536000, 1, true},
		{"3yr", 94608000, 3, true},
		{"zero", 0, 0, false},
		{"negative", -1, 0, false},
		{"5yr", 5 * 31536000, 0, false},
		{"90 days", 90 * 86400, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, ok := durationToTerm(tc.seconds)
			assert.Equal(t, tc.term, term)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestNormalizePayment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		out  string
		ok   bool
	}{
		// SDK-typed string variants ("All Upfront" / ...).
		{"rds All Upfront", "All Upfront", "all-upfront", true},
		{"rds Partial Upfront", "Partial Upfront", "partial-upfront", true},
		{"rds No Upfront", "No Upfront", "no-upfront", true},

		// OpenSearch enum stringer variant (ALL_UPFRONT).
		{"enum ALL_UPFRONT", "ALL_UPFRONT", "all-upfront", true},
		{"enum PARTIAL_UPFRONT", "PARTIAL_UPFRONT", "partial-upfront", true},
		{"enum NO_UPFRONT", "NO_UPFRONT", "no-upfront", true},

		// Already-canonical lowercase-kebab form.
		{"canonical all-upfront", "all-upfront", "all-upfront", true},
		{"canonical partial-upfront", "partial-upfront", "partial-upfront", true},
		{"canonical no-upfront", "no-upfront", "no-upfront", true},

		// Mixed-case / weird spacing — still normalizes.
		{"mixed case", "aLl UpFrOnT", "all-upfront", true},
		{"leading/trailing whitespace", "  All Upfront  ", "all-upfront", true},
		{"underscore lowercase", "all_upfront", "all-upfront", true},

		// Legacy utilization-based offerings must be rejected.
		{"light utilization", "Light Utilization", "", false},
		{"medium utilization", "Medium Utilization", "", false},
		{"heavy utilization", "Heavy Utilization", "", false},
		{"light lowercase", "lightutilization", "", false},

		// Unknown payment option — dropped.
		{"unknown", "Something Else", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizePayment(tc.in)
			assert.Equal(t, tc.out, got)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestStripNonAlnumLower(t *testing.T) {
	cases := []struct {
		in  string
		out string
	}{
		{"All Upfront", "allupfront"},
		{"ALL_UPFRONT", "allupfront"},
		{"  all-upfront ", "allupfront"},
		{"a1b2c3", "a1b2c3"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.out, stripNonAlnumLower(tc.in))
		})
	}
}
