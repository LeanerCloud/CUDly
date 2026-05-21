package common

import (
	"strconv"
	"strings"
	"time"
)

// isAllowedIDChar reports whether r is allowed in an AWS reservation ID
// (ASCII letter, digit, or hyphen).
func isAllowedIDChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
}

// SanitizeReservationID returns an identifier safe for AWS reservation/reserved-instance
// ID or name fields: only ASCII letters, digits, and hyphens; no leading/trailing
// hyphen; no consecutive hyphens. Dots are replaced with hyphens. If the result
// would be empty, returns fallbackPrefix plus a Unix timestamp.
func SanitizeReservationID(id, fallbackPrefix string) string {
	var b strings.Builder
	for _, r := range id {
		if isAllowedIDChar(r) {
			b.WriteRune(r)
		} else if r == '.' {
			b.WriteRune('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" {
		s = fallbackPrefix + strconv.FormatInt(time.Now().Unix(), 10)
	}
	return s
}

// idempotencyIDTokenLen is how many leading hex characters of the
// idempotency token are folded into a derived reservation ID. 40 hex chars =
// 160 bits, collision-free at any realistic purchase volume, and short enough
// to keep the prefixed result under every AWS reserved-instance/node ID length
// limit (RDS being the tightest).
const idempotencyIDTokenLen = 40

// IdempotentReservationID derives a deterministic, AWS-safe reservation ID from
// an idempotency token (issue #641). The same token always yields the same ID,
// so a re-driven purchase reuses the identical customer-supplied reservation ID
// and AWS rejects the duplicate server-side (RDS/ElastiCache/MemoryDB each
// return a *AlreadyExists* fault). Returns "" when token is empty so the caller
// keeps its prior non-idempotent (timestamp-based) ID behaviour for call sites
// that supply no token (e.g. the CLI path).
//
// prefix should be a short, lowercase, hyphen-terminated service tag (e.g.
// "rds-id-") so the reservation is identifiable in the console; the token is
// hex so the result needs no further sanitisation beyond SanitizeReservationID's
// invariants.
func IdempotentReservationID(prefix, token string) string {
	if token == "" {
		return ""
	}
	if len(token) > idempotencyIDTokenLen {
		token = token[:idempotencyIDTokenLen]
	}
	return SanitizeReservationID(prefix+token, prefix)
}
