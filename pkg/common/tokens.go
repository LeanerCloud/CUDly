package common

import (
	"crypto/rand"
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
