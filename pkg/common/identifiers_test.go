package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdempotentReservationID_DeterministicAndSafe(t *testing.T) {
	token := DeriveIdempotencyToken("exec-1", 0)

	a := IdempotentReservationID("rds-id-", token)
	b := IdempotentReservationID("rds-id-", token)

	assert.Equal(t, a, b, "same token must yield the same reservation ID")
	assert.True(t, strings.HasPrefix(a, "rds-id-"), "must carry the prefix for console identifiability")
	assert.NotContains(t, a, "--", "must not contain consecutive hyphens")
	assert.False(t, strings.HasSuffix(a, "-"), "must not end with a hyphen")
	// prefix (7) + 40 hex chars = 47, well under RDS's tightest ID length cap.
	assert.LessOrEqual(t, len(a), 60, "must stay under the tightest AWS reservation-ID length cap")
}

func TestIdempotentReservationID_DistinctTokensDistinctIDs(t *testing.T) {
	id0 := IdempotentReservationID("rds-id-", DeriveIdempotencyToken("exec-1", 0))
	id1 := IdempotentReservationID("rds-id-", DeriveIdempotencyToken("exec-1", 1))
	assert.NotEqual(t, id0, id1, "different recs in an execution must get different IDs")
}

func TestIdempotentReservationID_EmptyTokenReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", IdempotentReservationID("rds-id-", ""),
		"empty token must yield empty so the caller keeps its non-idempotent fallback")
}
