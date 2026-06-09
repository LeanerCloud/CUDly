package commitmentopts

import "strings"

// oneYearSeconds and threeYearSeconds are the only reservation durations
// AWS currently sells across the services we probe. Anything else (90-day
// heavy-utilization RIs, 5-year Redshift offerings on the rare occasion
// they exist) is dropped — the frontend only exposes 1yr/3yr in the UI so
// other durations have no place to surface.
const (
	oneYearSeconds   int64 = 31536000
	threeYearSeconds int64 = 94608000
)

// durationToTerm maps a duration-in-seconds (as returned by the AWS
// Describe*Offerings APIs) to a term in whole years. Returns (0, false)
// for anything outside {1yr, 3yr}; callers drop those Combos.
func durationToTerm(seconds int64) (int, bool) {
	switch seconds {
	case oneYearSeconds:
		return 1, true
	case threeYearSeconds:
		return 3, true
	default:
		return 0, false
	}
}

// normalizePayment canonicalizes the messy zoo of payment-option spellings
// AWS uses across services into one of our three tokens. Input may be:
//
//   - "All Upfront" / "Partial Upfront" / "No Upfront"
//     (RDS/ElastiCache/Redshift/MemoryDB OfferingType strings)
//   - "ALL_UPFRONT" / "PARTIAL_UPFRONT" / "NO_UPFRONT"
//     (OpenSearch's ReservedInstancePaymentOption enum stringer)
//   - already-canonical "all-upfront" etc.
//
// It explicitly rejects legacy pre-2011 ElastiCache/EC2 utilization tokens
// ("Light Utilization" / "Medium Utilization" / "Heavy Utilization") — those
// predate the modern (term, payment) model and there is no sensible mapping
// into it.
func normalizePayment(raw string) (string, bool) {
	token := stripNonAlnumLower(raw)
	switch token {
	case "allupfront":
		return "all-upfront", true
	case "partialupfront":
		return "partial-upfront", true
	case "noupfront":
		return "no-upfront", true
	case "lightutilization", "mediumutilization", "heavyutilization":
		// Legacy utilization-based offerings — deliberately rejected.
		return "", false
	default:
		return "", false
	}
}

// stripNonAlnumLower returns s lowercased with every non-alphanumeric
// character removed. It is the canonical form used to compare the many
// payment-option spellings AWS APIs emit.
func stripNonAlnumLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			// Drop spaces, hyphens, underscores, and everything else.
		}
	}
	return b.String()
}
