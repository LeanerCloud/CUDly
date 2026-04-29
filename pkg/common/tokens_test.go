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
