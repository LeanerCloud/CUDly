// Package reservations provides shared helpers for Azure Reservations API operations.
package reservations

import "strings"

// isAllowedDisplayNameChar reports whether r is in Azure's DisplayName allowlist
// ([A-Za-z0-9-]). Underscores are emitted by the sanitizer, not passed through here.
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
	if len(result) > 64 {
		// All output chars are ASCII so byte index == rune index.
		result = result[:64]
	}
	return result
}
