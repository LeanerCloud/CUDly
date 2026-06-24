package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// awsReservationNameMaxLen is the tightest reservation-name length cap across
// the 5 AWS services that accept a customer-supplied reservation ID/name:
// RDS (ReservedDBInstanceId), ElastiCache (ReservedCacheNodeId), MemoryDB
// (ReservationId), and OpenSearch (ReservationName) all advertise a 60-char
// cap. Redshift doesn't accept a name field — its rich descriptors travel
// as tags instead. 60 is therefore the safe upper bound for any name the
// builder produces.
const awsReservationNameMaxLen = 60

// ReservationNameFields carries the inputs needed by BuildReservationName.
//
// Now and randSource are exposed so tests can pin time and randomness for
// deterministic assertions. Production callers leave randSource nil (the
// builder then uses crypto/rand) and pass time.Now().
type ReservationNameFields struct {
	Now          time.Time
	Service      string
	Region       string
	ResourceType string
	Term         string
	Payment      string
	randSource   []byte
	Count        int
}

// WithRandSource returns a copy of f with the given bytes used as the
// random suffix source (test hook). Production code does not call this.
func (f *ReservationNameFields) WithRandSource(b []byte) ReservationNameFields {
	cp := *f
	cp.randSource = b
	return cp
}

// BuildReservationName composes a rich, parseable identifier for an AWS
// reservation purchase. The format mirrors the Azure DisplayName format
// from #686 so cross-cloud parsers can share logic:
//
//	{svc}-{region}-{sku}-{count}x-{term}-{paymt}-{ts}-{rand}
//
// e.g. "opensearch-us-east-1-r6gd-large-search-3x-1yr-allup-20260521T002019-a1b2c3d4"
// (which then gets truncated to fit the 60-char cap — see below).
//
// The result is always sanitized via SanitizeReservationID for AWS
// reservation-name allowlists ([a-zA-Z0-9-], so underscores in SKU names
// become '-') and never longer than awsReservationNameMaxLen (60). If the
// composed string would exceed 60, optional tail fields are progressively
// dropped: random suffix first, then timestamp, then payment-option. The
// service code, region, SKU, count, and term are NEVER dropped — those are
// the high-signal segments operators rely on to identify the reservation
// in the AWS console.
//
// fallbackPrefix is the prefix passed to SanitizeReservationID for the
// unreachable empty-output fallback (e.g. "rds-reserved-"); it preserves
// the prior call-site behavior at every service when the builder ever
// emits an unsanitizable input.
func BuildReservationName(f *ReservationNameFields, fallbackPrefix string) string {
	if f == nil {
		// Nil fields are a programmer error (every call site passes &localStruct).
		// Return the caller's fallbackPrefix rather than panicking -- the name is
		// only a console-display tag, so the same unsanitizable-input fallback
		// path keeps an exported helper from crashing on a nil pointer argument.
		return SanitizeReservationID("", fallbackPrefix)
	}
	svc := normalizeReservationSegment(f.Service)
	region := normalizeReservationSegment(f.Region)
	sku := normalizeReservationSegment(f.ResourceType)
	count := fmt.Sprintf("%dx", f.Count)
	term := normalizeReservationTerm(f.Term)
	paymt := normalizeReservationPayment(f.Payment)
	tsTime := f.Now
	if tsTime.IsZero() {
		tsTime = time.Now()
	}
	ts := tsTime.UTC().Format("20060102T150405")
	randHex := generateReservationRandSuffix(f.randSource)

	// Required segments (order matters — never dropped, never reordered).
	required := []string{svc, region, sku, count, term}

	// Optional tail segments, in drop priority. The slice order here is
	// "keep" order; dropping happens from the right.
	tail := []string{paymt, ts, randHex}

	// Try full -> drop random -> drop timestamp -> drop payment.
	for keep := len(tail); keep >= 0; keep-- {
		segments := append([]string{}, required...)
		segments = append(segments, tail[:keep]...)
		candidate := joinReservationNonEmpty(segments, "-")
		// Defensive sanitization: even though each segment is already
		// allowlist-conformant, SanitizeReservationID guarantees the
		// invariant (no leading/trailing/double hyphens) in one place
		// rather than relying on every caller.
		candidate = SanitizeReservationID(candidate, fallbackPrefix)
		if len(candidate) <= awsReservationNameMaxLen {
			return candidate
		}
	}

	// All optional segments dropped and we still bust the cap — the SKU
	// is pathologically long (the other required segments are bounded:
	// service codes are short, regions ≤14 chars, count "Nx" is 2–3 chars,
	// term is 3 chars). Truncate the SKU itself (not the joined output)
	// so count and term still survive the final assembly.
	overflow := len(joinReservationNonEmpty(required, "-")) - awsReservationNameMaxLen
	if overflow > 0 {
		// Each separator counts; truncating N chars from the SKU shrinks
		// the joined string by exactly N. Leave at least 1 SKU char so
		// the segment doesn't collapse to "" and produce a "--" run.
		newLen := len(sku) - overflow
		if newLen < 1 {
			newLen = 1
		}
		sku = sku[:newLen]
		required[2] = sku
	}
	out := SanitizeReservationID(joinReservationNonEmpty(required, "-"), fallbackPrefix)
	if len(out) > awsReservationNameMaxLen {
		// Last-resort hard cap if the SKU truncation above wasn't enough
		// (e.g. a region name longer than expected). All output is ASCII
		// so byte index == rune index.
		out = strings.TrimRight(out[:awsReservationNameMaxLen], "-")
	}
	return out
}

// normalizeReservationSegment strips disallowed characters from a single
// segment via the existing SanitizeReservationID rules. Dots become hyphens
// (so "db.t4g.medium" becomes "db-t4g-medium"), and underscores are dropped
// entirely. Leading/trailing hyphens and double-hyphen runs are collapsed
// by SanitizeReservationID itself.
func normalizeReservationSegment(s string) string {
	return SanitizeReservationID(s, "")
}

// normalizeReservationTerm maps "1"/"1yr"/"1y"/"P1Y" -> "1yr" and similar
// for 3-year. Anything else falls back to a sanitized passthrough.
func normalizeReservationTerm(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "1yr", "1y", "p1y":
		return "1yr"
	case "3", "3yr", "3y", "p3y":
		return "3yr"
	default:
		return normalizeReservationSegment(s)
	}
}

// normalizeReservationPayment maps known payment-option strings to short
// forms. Unknown values are sanitized and truncated to keep the segment
// short (≤6 chars).
func normalizeReservationPayment(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all-upfront", "allupfront", "upfront":
		return "allup"
	case "no-upfront", "noupfront":
		return "noup"
	case "partial-upfront", "partialupfront", "partial":
		return "partup"
	case "":
		return ""
	default:
		out := normalizeReservationSegment(s)
		if len(out) > 6 {
			out = out[:6]
		}
		return out
	}
}

// joinReservationNonEmpty joins parts with sep, skipping empty strings so
// callers don't produce double-separator artifacts when an optional segment
// is missing.
func joinReservationNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// generateReservationRandSuffix returns 8 hex chars from src (test hook) or
// crypto/rand. If randomness can't be obtained (extremely unlikely on
// supported platforms) returns an empty string — the builder treats that as
// a dropped suffix.
func generateReservationRandSuffix(src []byte) string {
	if len(src) >= 4 {
		return hex.EncodeToString(src[:4])
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
