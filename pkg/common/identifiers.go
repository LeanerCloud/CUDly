package common

import (
	"strconv"
	"strings"
	"time"
)

// SanitizeReservationID returns an identifier safe for AWS reservation/reserved-instance
// ID or name fields: only ASCII letters, digits, and hyphens; no leading/trailing
// hyphen; no consecutive hyphens. Dots are replaced with hyphens. If the result
// would be empty, returns fallbackPrefix plus a Unix timestamp.
func SanitizeReservationID(id, fallbackPrefix string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
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
