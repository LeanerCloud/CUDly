// Package reservations provides shared helpers for Azure Reservations API operations.
package reservations

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// azureDisplayNameMaxLen is Azure's hard cap on Reservation DisplayName length.
// Azure rejects longer values with HTTP 400 DisplayNameInvalid.
const azureDisplayNameMaxLen = 64

// isAllowedDisplayNameChar reports whether r is in Azure's base allowlist
// [A-Za-z0-9-]. Underscores are handled separately by the caller
// (SanitizeDisplayName passes '_' through verbatim alongside the chars
// matched here).
func isAllowedDisplayNameChar(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}

// SanitizeDisplayName returns s with any character outside [A-Za-z0-9_-]
// replaced by '_', truncated to 64 chars. Azure rejects DisplayName fields
// that don't match this allowlist with HTTP 400 DisplayNameInvalid.
// Runs of non-conforming characters are collapsed into a single '_'.
func SanitizeDisplayName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastWasUnderscore := false
	for _, r := range s {
		if isAllowedDisplayNameChar(r) || r == '_' {
			b.WriteRune(r)
			lastWasUnderscore = r == '_'
		} else if !lastWasUnderscore {
			b.WriteByte('_')
			lastWasUnderscore = true
		}
	}
	result := b.String()
	if len(result) > azureDisplayNameMaxLen {
		// All output chars are ASCII so byte index == rune index.
		result = result[:azureDisplayNameMaxLen]
	}
	return result
}

// DisplayNameFields carries the inputs needed by BuildDisplayName.
//
// Now and randSource are exposed so tests can pin time and randomness for
// deterministic assertions. Production callers leave randSource nil (the
// builder then uses crypto/rand) and pass time.Now().
type DisplayNameFields struct {
	// Service is a short identifier for the Azure service, e.g. "vm",
	// "redis", "cosmos", "sql", "search". Set per-call by the service
	// client; the builder treats it as opaque and sanitizes it.
	Service string

	// Region is the Azure location string (e.g. "eastus", "westeurope").
	Region string

	// ResourceType is the Azure SKU name (e.g. "Standard_D2a_v4").
	ResourceType string

	// Term is the commitment term, normalized to "1yr" / "3yr" by upstream
	// recommendation parsers. Pass through as-is; the builder collapses
	// it to "1yr"/"3yr" when possible, and sanitizes otherwise.
	Term string

	// Payment is the payment option string from the recommendation
	// ("all-upfront", "upfront", "no-upfront", "monthly", "partial-upfront").
	// The builder normalizes to a short form ("allup", "noup", "partup",
	// "monthly") so the segment stays under 8 chars.
	Payment string

	// Now is the timestamp baseline. Tests inject a fixed value for
	// determinism; production callers should pass time.Now(). A zero
	// time.Time is replaced with time.Now() by the builder so the
	// timestamp segment never emits the placeholder "00010101T000000".
	Now time.Time

	// randSource is an optional 4-byte source for the random suffix.
	// When nil (production), the builder reads from crypto/rand. Tests set
	// it via WithRandSource to make output deterministic.
	randSource []byte

	// Count is the reservation quantity. Always rendered as "{N}x".
	// Placed last to minimize GC scan range (no pointer; all pointer fields precede it).
	Count int
}

// WithRandSource returns a copy of f with the given bytes used as the
// random suffix source (test hook). Production code does not call this.
//
//nolint:gocritic // hugeParam: value receiver is intentional -- copy-on-update is the semantic contract for this immutable builder pattern
func (f DisplayNameFields) WithRandSource(b []byte) DisplayNameFields {
	f.randSource = b
	return f
}

// BuildDisplayName composes a rich, parseable identifier for an Azure
// reservation purchase. The format mirrors the AWS RI CSV's ReservationId
// column shape:
//
//	{svc}-{region}-{sku}-{count}x-{term}-{paymt}-{ts}-{rand}
//
// e.g. "vm-eastus-Standard_D2a_v4-1x-1yr-allup-20260522T190000-a1b2c3d4".
//
// The result is always sanitized to [A-Za-z0-9_-] and never longer than
// 64 characters. If the composed string would exceed 64, fields are
// progressively dropped from the tail (random suffix first, then
// timestamp, then payment-option) until it fits. The service code,
// region, SKU, count, and term are NEVER dropped -- those are the
// high-signal segments operators rely on to identify the reservation in
// the Azure portal.
//
//nolint:gocritic // hugeParam: callers compose DisplayNameFields inline; switching to pointer would require heap allocation at every call site
func BuildDisplayName(f DisplayNameFields) string {
	svc := normalizeSegment(f.Service)
	region := normalizeSegment(f.Region)
	sku := normalizeSegment(f.ResourceType)
	count := fmt.Sprintf("%dx", f.Count)
	term := normalizeTerm(f.Term)
	paymt := normalizePayment(f.Payment)
	// Guard against a zero-value Now (which would emit the nonsensical
	// "00010101T000000"). Tests that want determinism pin Now explicitly;
	// production callers that forget get a real timestamp, not a placeholder.
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	ts := now.UTC().Format("20060102T150405")
	randHex := generateRandSuffix(f.randSource)

	// Required segments (order matters — never dropped, never reordered).
	required := []string{svc, region, sku, count, term}

	// Optional tail segments, in drop priority. The slice order here is
	// "keep" order; dropping happens from the right.
	tail := []string{paymt, ts, randHex}

	// Try full -> drop random -> drop timestamp -> drop payment. Check the
	// PRE-sanitized length so the cap actually gates segment-dropping; calling
	// SanitizeDisplayName inside the loop would make the cap vacuously true
	// (the sanitizer hard-truncates to 64) and short-circuit the drop logic.
	// Each segment is already allowlist-conformant via normalizeSegment, so we
	// only need to sanitize once on the exit path as a defensive invariant.
	for keep := len(tail); keep >= 0; keep-- {
		segments := append([]string{}, required...)
		segments = append(segments, tail[:keep]...)
		candidate := joinNonEmpty(segments, "-")
		if len(candidate) <= azureDisplayNameMaxLen {
			return SanitizeDisplayName(candidate)
		}
	}

	// All optional segments dropped and we still bust the cap — fall back
	// to truncating the joined required segments via SanitizeDisplayName.
	// This path is reachable only with pathologically long inputs (e.g.
	// an impossibly long SKU name) but the builder must never return >64.
	return SanitizeDisplayName(joinNonEmpty(required, "-"))
}

// normalizeSegment strips disallowed characters from a single segment.
// The dash separator is the only allowlist character we reserve for joins,
// so embedded dashes inside a segment are converted to underscores to
// avoid ambiguity at parse-time (consumers split on "-").
func normalizeSegment(s string) string {
	// Replace dashes with underscores first so they don't collide with
	// the join separator, then sanitize the rest normally.
	s = strings.ReplaceAll(s, "-", "_")
	return SanitizeDisplayName(s)
}

// normalizeTerm maps "1"/"1yr"/"P1Y" -> "1yr" and "3"/"3yr"/"P3Y" -> "3yr".
// Anything else falls back to a sanitized passthrough.
func normalizeTerm(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "1yr", "1y", "p1y":
		return "1yr"
	case "3", "3yr", "3y", "p3y":
		return "3yr"
	default:
		return normalizeSegment(s)
	}
}

// normalizePayment maps known payment-option strings to short forms.
// Unknown values are sanitized and truncated to keep the segment ≤6 chars.
func normalizePayment(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all-upfront", "allupfront", "upfront":
		return "allup"
	case "no-upfront", "noupfront":
		return "noup"
	case "partial-upfront", "partialupfront", "partial":
		return "partup"
	case "monthly":
		return "monthly"
	case "":
		return ""
	default:
		out := normalizeSegment(s)
		if len(out) > 6 {
			out = out[:6]
		}
		return out
	}
}

// joinNonEmpty joins parts with sep, skipping any empty strings so callers
// don't produce double-separator artifacts ("svc--region") when an optional
// segment is missing.
func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// generateRandSuffix returns 8 hex chars from src (test hook) or crypto/rand.
// If src is non-nil but shorter than 4 bytes it is treated as if nil and
// crypto/rand is used as the fallback; callers that want deterministic output
// must pass at least 4 bytes. If randomness can't be obtained (extremely
// unlikely on supported platforms), returns an empty string, which the
// builder treats as a dropped suffix.
func generateRandSuffix(src []byte) string {
	if len(src) >= 4 {
		return hex.EncodeToString(src[:4])
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
