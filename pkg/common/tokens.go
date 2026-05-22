package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GenerateApprovalToken returns a 32-byte cryptographically secure random
// token, hex-encoded (64 chars). Used for purchase + RI exchange + plan
// approval flows where the token is the only credential in a one-click
// email link.
//
// Why not uuid.New().String()? UUID v4 is 122 bits of entropy in a known
// format (8-4-4-4-12 hex with version + variant nibbles fixed). 32 random
// bytes provide a full 256 bits of unpredictability and a uniform output
// space, making token guessing computationally hopeless.
func GenerateApprovalToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate approval token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// DeriveIdempotencyToken returns a deterministic token for a single purchase
// recommendation, derived from the owning execution's ID and the recommendation's
// index within that execution. The same (executionID, recIndex) pair always
// yields the same token, so a re-driven purchase of a stranded execution (issue
// #636) reuses the identical token: AWS Savings Plans dedupe on it natively via
// CreateSavingsPlanInput.ClientToken, and the EC2 RI client uses it as a dedupe
// tag (IdempotencyTagKey). The output is a 64-char hex SHA-256 digest, which fits
// the AWS ClientToken 64-character limit exactly.
//
// Unlike GenerateApprovalToken this is intentionally NOT random: idempotency
// requires the token be reproducible from durable inputs (execution_id + index),
// which both survive a strand-and-re-drive. It is not a credential, so the lack
// of unpredictability is by design.
func DeriveIdempotencyToken(executionID string, recIndex int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", executionID, recIndex)))
	return hex.EncodeToString(sum[:])
}

// MaskToken returns a log-safe representation of an idempotency/approval token:
// the first 8 characters followed by an ellipsis, never the full value. This
// keeps just enough of the prefix to correlate log lines for a single purchase
// while avoiding emitting the whole caller-supplied token into persistent logs
// (a stable per-execution identifier that should not leak verbatim). An empty
// token yields "(none)"; a token of 8 chars or fewer is returned unchanged
// since there is nothing left to redact.
func MaskToken(token string) string {
	if token == "" {
		return "(none)"
	}
	if len(token) <= 8 {
		return token
	}
	return token[:8] + "..."
}
