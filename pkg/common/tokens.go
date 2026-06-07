package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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
// token yields "(none)". A token of 8 chars or fewer is fully redacted to
// "(redacted)" rather than echoed: an 8-char prefix of an 8-char input is the
// whole value, so for short inputs (e.g. a short secret a future caller might
// pass) nothing of the token is emitted.
func MaskToken(token string) string {
	if token == "" {
		return "(none)"
	}
	if len(token) <= 8 {
		return "(redacted)"
	}
	return token[:8] + "..."
}

// IdempotencyGUID formats an idempotency token as a deterministic canonical GUID
// (8-4-4-4-12 lowercase hex) for use as an Azure reservationOrderID (issue #641).
// The Azure Reservations API path is reservationOrders/{guid} and a PUT is
// idempotent on a stable order ID, so deriving the GUID from the token makes a
// re-drive re-PUT the same order rather than create a second reservation.
//
// It uses the first 32 hex characters (128 bits) of the token, which is itself a
// SHA-256 hex digest, so the GUID is deterministic and collision-free at any
// realistic purchase volume. Returns "" when token is shorter than 32 hex chars
// (e.g. empty) so callers keep their prior non-idempotent ID behaviour.
func IdempotencyGUID(token string) string {
	if len(token) < 32 {
		return ""
	}
	h := strings.ToLower(token[:32])
	if _, err := hex.DecodeString(h); err != nil {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// ReservationOrderID returns the Azure reservationOrderID to PUT for a purchase:
// the deterministic GUID derived from token when one is supplied (issue #641, so
// a re-drive re-PUTs the same idempotent order), otherwise fallback (the caller's
// prior non-idempotent ID, e.g. a random GUID or a timestamp). Centralising the
// choice keeps each executor's PurchaseCommitment a single statement and avoids
// repeating the same empty-token guard across every Azure service.
func ReservationOrderID(token, fallback string) string {
	if guid := IdempotencyGUID(token); guid != "" {
		return guid
	}
	return fallback
}
