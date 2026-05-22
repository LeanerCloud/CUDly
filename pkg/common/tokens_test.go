package common

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateApprovalToken_LengthAndHex(t *testing.T) {
	tok, err := GenerateApprovalToken()
	require.NoError(t, err)
	assert.Len(t, tok, 64, "32 bytes hex-encoded should be 64 chars")

	raw, err := hex.DecodeString(tok)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

func TestGenerateApprovalToken_Uniqueness(t *testing.T) {
	// Generating 100 tokens should never collide.
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		tok, err := GenerateApprovalToken()
		require.NoError(t, err)
		_, dup := seen[tok]
		assert.False(t, dup, "duplicate token generated on iteration %d", i)
		seen[tok] = struct{}{}
	}
}

func TestGenerateApprovalToken_NotZeroPrefix(t *testing.T) {
	// 64 zero hex chars would be a sentinel for a clearly broken RNG.
	tok, err := GenerateApprovalToken()
	require.NoError(t, err)
	assert.NotEqual(t, "0000000000000000000000000000000000000000000000000000000000000000", tok)
}

func TestDeriveIdempotencyToken_StableAcrossReDrive(t *testing.T) {
	// The core idempotency guarantee (issue #636): re-deriving the token for
	// the SAME execution + rec index must yield the IDENTICAL value, so a
	// re-driven purchase reuses it and AWS / the EC2 dedupe guard collapse it
	// onto the original commitment instead of creating a second one.
	first := DeriveIdempotencyToken("exec-abc-123", 2)
	second := DeriveIdempotencyToken("exec-abc-123", 2)
	assert.Equal(t, first, second, "re-drive of same execution/rec must produce the same token")
}

func TestDeriveIdempotencyToken_DiffersByExecutionAndIndex(t *testing.T) {
	// Different recs within an execution, and the same rec index across
	// executions, must NOT collide, or one purchase would suppress another.
	base := DeriveIdempotencyToken("exec-abc-123", 0)
	assert.NotEqual(t, base, DeriveIdempotencyToken("exec-abc-123", 1), "different rec index must differ")
	assert.NotEqual(t, base, DeriveIdempotencyToken("exec-xyz-999", 0), "different execution must differ")
}

func TestDeriveIdempotencyToken_FitsClientTokenLimit(t *testing.T) {
	// AWS ClientToken (and the SP ClientToken) cap at 64 chars; a SHA-256 hex
	// digest is exactly 64, so the token can be used verbatim without truncation.
	tok := DeriveIdempotencyToken("exec-abc-123", 7)
	assert.Len(t, tok, 64)
	raw, err := hex.DecodeString(tok)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

func TestMaskToken_NeverEmitsFullToken(t *testing.T) {
	// CodeRabbit PR #652: the "already exists" skip-purchase log lines must not
	// emit the raw idempotency token (a stable per-execution identifier). The
	// masked form keeps only an 8-char prefix for log correlation.
	full := DeriveIdempotencyToken("exec-abc-123", 0) // 64-char hex digest
	masked := MaskToken(full)

	assert.NotEqual(t, full, masked, "masked form must differ from the raw token")
	assert.NotContains(t, masked, full, "masked output must not contain the full token")
	assert.Less(t, len(masked), len(full), "masked output must be shorter than the raw token")
	assert.Equal(t, full[:8]+"...", masked, "masked form is an 8-char prefix plus ellipsis")
	assert.Len(t, masked, 11, "8 prefix chars + 3-char ellipsis")
}

func TestMaskToken_EmptyAndShort(t *testing.T) {
	assert.Equal(t, "(none)", MaskToken(""), "empty token must be reported as (none)")
	assert.Equal(t, "abc", MaskToken("abc"), "tokens of <=8 chars have nothing to redact")
	assert.Equal(t, "12345678", MaskToken("12345678"), "exactly 8 chars is returned unchanged")
	assert.Equal(t, "12345678...", MaskToken("123456789"), "9 chars is truncated to 8 + ellipsis")
}
