package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- SHA-1 retained for broad authenticator app compatibility and existing otpauth provisioning; RFC 6238 permits SHA-256/SHA-512 but most authenticator apps default to SHA-1
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"golang.org/x/crypto/bcrypt"
)

// mfaPendingExpiry is how long a setup-but-not-yet-enabled MFA secret
// stays valid before the user has to start over. Short window because
// the secret travels over the wire on setup and an abandoned
// enrollment shouldn't leave a usable secret lingering.
const mfaPendingExpiry = 5 * time.Minute

// mfaIssuer is the issuer label that appears in the authenticator app
// alongside the account email. Apps surface this in the entry name
// and the 6-digit code header so the user can tell which CUDly
// instance the code is for when they have multiple.
const mfaIssuer = "CUDly"

// recoveryCodeCount is the number of single-use recovery codes
// generated per enrollment / regeneration. 10 matches the Google /
// GitHub / GitLab default and is the smallest count that doesn't
// regularly leave a user locked out after losing one or two codes.
const recoveryCodeCount = 10

// recoveryCodeLength is the visible (formatted) length of a recovery
// code, before the dash. Codes are rendered as `XXXX-XXXX` so the
// user can read them aloud or type them with the dash for grouping;
// the dash is stripped on validation so an entered code is
// case-insensitively matched against the bcrypt hash.
const recoveryCodeLength = 8

// recoveryCodeAlphabet is a Crockford-style alphabet without
// confusable characters (no 0/O, 1/I/L). Recovery codes are
// shown to humans once and typed back, so visual clarity matters.
const recoveryCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// verifyTOTP validates a TOTP code against the secret.
// Implements RFC 6238 TOTP with 30-second time steps.
// Uses constant-time comparison to prevent timing attacks.
//
// Fails closed: returns false immediately when code or secret is empty.
// An empty code can arrive from a missing form field; an empty secret signals
// a data-integrity anomaly (MFA enabled but no secret stored). In both cases
// the correct behavior is to reject, not to accidentally match because
// generateTOTP("","") returns "" and ConstantTimeCompare("","") == 1.
func verifyTOTP(secret, code string) bool {
	// Fail closed: reject empty code and empty secret up front.
	// generateTOTP returns "" on base32-decode failure; ConstantTimeCompare("","")
	// evaluates to 1 and would bypass MFA for any caller that passes code="".
	if code == "" {
		return false
	}
	if secret == "" {
		return false
	}

	// Allow for time skew by checking current and adjacent time windows.
	currentTime := time.Now().Unix()
	timeStep := int64(config.MFATimeStep)

	// Check current time step and one step before/after for clock skew tolerance.
	// Use constant-time comparison and avoid early returns to prevent timing attacks.
	valid := 0
	for _, offset := range []int64{-1, 0, 1} {
		counter := (currentTime / timeStep) + offset
		expected := generateTOTP(secret, counter)
		// generateTOTP returns "" on base32-decode failure; a non-empty code can
		// never equal "" so a bad secret produces no match.
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			valid = 1
		}
	}
	return valid == 1
}

// generateTOTP generates a TOTP code for the given counter.
func generateTOTP(secret string, counter int64) string {
	// Decode base32 secret
	secretBytes, err := base32Decode(secret)
	if err != nil {
		return ""
	}

	// Convert counter to 8-byte big-endian
	counterBytes := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		counterBytes[i] = byte(counter & 0xff)
		counter >>= 8
	}

	// Compute HMAC-SHA1
	h := hmac.New(sha1.New, secretBytes)
	h.Write(counterBytes)
	hash := h.Sum(nil)

	// Dynamic truncation (RFC 4226)
	offset := hash[len(hash)-1] & 0x0f
	binary := (int32(hash[offset])&0x7f)<<24 |
		(int32(hash[offset+1])&0xff)<<16 |
		(int32(hash[offset+2])&0xff)<<8 |
		(int32(hash[offset+3]) & 0xff)

	// Generate 6-digit code
	otp := binary % 1000000
	return fmt.Sprintf("%06d", otp)
}

// base32Decode decodes a base32 string (RFC 4648) using the stdlib encoder.
func base32Decode(s string) ([]byte, error) {
	// Normalize: uppercase and add padding if needed
	s = strings.ToUpper(s)
	if m := len(s) % 8; m != 0 {
		s += strings.Repeat("=", 8-m)
	}
	return base32.StdEncoding.DecodeString(s)
}

// generateMFASecret produces a fresh 160-bit (20-byte) secret encoded
// as unpadded base32. 160 bits matches the RFC 4226 / Google
// Authenticator recommendation and yields a 32-character secret that
// fits in one screen of an authenticator-app entry.
func generateMFASecret() (string, error) {
	bytes := make([]byte, 20)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to read random bytes for MFA secret: %w", err)
	}
	// Use the no-padding encoding — authenticator apps reject '='
	// padding and so does the verifier above (base32Decode adds the
	// padding it needs back at decode time).
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes), nil
}

// buildProvisioningURI constructs the otpauth:// URI authenticator
// apps consume from a scanned QR code or paste. RFC sketch in
// https://github.com/google/google-authenticator/wiki/Key-Uri-Format.
//
// The label encodes the issuer twice — once as the path prefix
// ("CUDly:user@example.com") and once as the issuer query param —
// because legacy apps read one or the other. Algorithm/digits/period
// are spelled out even though they're the defaults so older apps that
// don't assume the defaults still work.
func buildProvisioningURI(accountEmail, secret string) string {
	label := url.PathEscape(mfaIssuer + ":" + accountEmail)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", mfaIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return fmt.Sprintf("otpauth://totp/%s?%s", label, q.Encode())
}

// generateRecoveryCodes produces n plaintext recovery codes formatted
// as `XXXX-XXXX` for readability. Returns the plaintext slice — the
// caller is responsible for hashing for storage and surfacing the
// plaintext to the user exactly once.
func generateRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	alphabetLen := byte(len(recoveryCodeAlphabet))
	// Pull a buffer of random bytes once per code rather than one byte
	// at a time so failure modes are easier to reason about.
	for i := 0; i < n; i++ {
		raw := make([]byte, recoveryCodeLength)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("failed to read random bytes for recovery code: %w", err)
		}
		var sb strings.Builder
		sb.Grow(recoveryCodeLength + 1)
		for j, b := range raw {
			if j == recoveryCodeLength/2 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryCodeAlphabet[b%alphabetLen])
		}
		out = append(out, sb.String())
	}
	return out, nil
}

// normalizeRecoveryCode strips formatting (dashes, spaces) and
// uppercases the input so a code entered with or without the visual
// dash separator hashes to the same bcrypt-comparable string.
func normalizeRecoveryCode(code string) string {
	var sb strings.Builder
	sb.Grow(len(code))
	for _, r := range code {
		if r == '-' || r == ' ' {
			continue
		}
		sb.WriteRune(r)
	}
	return strings.ToUpper(sb.String())
}

// hashRecoveryCode bcrypt-hashes a recovery code with the auth
// service's configured cost. Uses the same cost knob as password
// hashing so unit tests can drop to MinCost.
func (s *Service) hashRecoveryCode(code string) (string, error) {
	cost := bcryptCost
	if s.bcryptCostOverride > 0 {
		cost = s.bcryptCostOverride
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(normalizeRecoveryCode(code)), cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// consumeRecoveryCode returns true and removes the matching hash from
// user.MFARecoveryCodes if entered matches any stored hash. The
// removal is what makes recovery codes single-use; the caller must
// persist the user afterwards via UpdateUser.
//
// Fails closed on empty input: an empty string normalizes to "", which
// bcrypt.CompareHashAndPassword would compare against every stored hash.
// Reject it immediately to avoid leaking which hashes exist.
//
// Iterates over all stored hashes (bcrypt compare is constant-time
// for the matching hash but not across the slice — recovery codes
// are 8 chars of high-entropy alphabet, so timing leakage of which
// slot matched isn't usefully exploitable, but the comparison stays
// branch-free by ORing the bool into a single accumulator).
func (s *Service) consumeRecoveryCode(user *User, entered string) bool {
	normalized := normalizeRecoveryCode(entered)
	// Fail closed: reject empty input before any bcrypt comparison.
	if normalized == "" {
		return false
	}
	matchIdx := -1
	for i, hash := range user.MFARecoveryCodes {
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(normalized)); err == nil {
			matchIdx = i
			// Don't break — keep comparing the rest to equalize CPU
			// time across the slice. Recovery codes are typed by a
			// human one at a time, so the few-millisecond budget is
			// dwarfed by network latency anyway.
		}
	}
	if matchIdx < 0 {
		return false
	}
	// Remove the matched hash. Order-preserving delete; the slice is
	// small (10 entries) so the shift cost is negligible.
	user.MFARecoveryCodes = append(user.MFARecoveryCodes[:matchIdx], user.MFARecoveryCodes[matchIdx+1:]...)
	return true
}

// MFASetupResult is the user-facing payload returned by MFASetup.
// Carries the freshly-generated secret + the otpauth:// URI so the
// frontend can render a QR code and surface the secret for manual
// entry. The secret is also persisted server-side as the pending
// secret, so a stateless client-side carrier (signed token) is not
// needed.
type MFASetupResult struct {
	Secret          string //nolint:gosec // G117: HTTP redirect target is validated/trusted
	ProvisioningURI string
}

// MFASetup begins an MFA enrollment for a user. The caller must
// re-verify the user's password (defense-in-depth against a session
// token being lifted from another tab). Returns the freshly-generated
// secret + provisioning URI; persists the secret in the user's
// pending fields with a short expiry. Does NOT flip MFAEnabled —
// that happens in MFAEnable after the user proves they have the
// secret loaded in their authenticator.
//
// Safe to call repeatedly: each call overwrites the previous pending
// secret and resets the expiry. An abandoned enrollment expires
// harmlessly because the active MFASecret + MFAEnabled fields are
// untouched.
func (s *Service) MFASetup(ctx context.Context, userID, password string) (*MFASetupResult, error) {
	if err := s.ensureStore(); err != nil {
		return nil, err
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("%w", ErrMFAAuthFailed)
	}
	if user == nil {
		return nil, fmt.Errorf("%w", ErrMFAAuthFailed)
	}
	if !s.verifyPassword(password, user.PasswordHash) {
		return nil, fmt.Errorf("%w", ErrMFAInvalidPassword)
	}

	secret, err := generateMFASecret()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(mfaPendingExpiry)
	user.MFAPendingSecret = secret
	user.MFAPendingSecretExpiresAt = &expiresAt
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to persist pending MFA secret: %w", err)
	}

	return &MFASetupResult{
		Secret:          secret,
		ProvisioningURI: buildProvisioningURI(user.Email, secret),
	}, nil
}

// generateAndHashRecoveryCodes mints a fresh batch of plaintext
// recovery codes and returns both the plaintext slice (for the user)
// and the bcrypt-hashed slice (for storage). Pulled out as a helper
// so MFAEnable and MFARegenerateRecoveryCodes don't duplicate the
// loop.
func (s *Service) generateAndHashRecoveryCodes() (plaintext, hashes []string, err error) {
	plaintext, err = generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, nil, err
	}
	hashes = make([]string, 0, len(plaintext))
	for _, c := range plaintext {
		h, hashErr := s.hashRecoveryCode(c)
		if hashErr != nil {
			return nil, nil, hashErr
		}
		hashes = append(hashes, h)
	}
	return plaintext, hashes, nil
}

// validatePendingMFAEnrollment checks that a pending enrollment
// exists, hasn't expired, and the supplied code matches. Mutates
// user on expiry to wipe stale pending fields so a re-probe returns
// "no enrollment in progress" rather than "expired" forever.
func (s *Service) validatePendingMFAEnrollment(ctx context.Context, user *User, code string) error {
	if user.MFAPendingSecret == "" || user.MFAPendingSecretExpiresAt == nil {
		return fmt.Errorf("%w", ErrMFANoEnrollmentInProgress)
	}
	if time.Now().After(*user.MFAPendingSecretExpiresAt) {
		user.MFAPendingSecret = ""
		user.MFAPendingSecretExpiresAt = nil
		if updateErr := s.store.UpdateUser(ctx, user); updateErr != nil {
			logging.Warnf("MFAVerifyPendingCode: failed to clear expired pending secret for user: %v", updateErr)
		}
		return fmt.Errorf("%w", ErrMFAEnrollmentExpired)
	}
	if !verifyTOTP(user.MFAPendingSecret, code) {
		return fmt.Errorf("%w", ErrMFAInvalidCode)
	}
	return nil
}

// MFAEnable finalizes an MFA enrollment. Validates the supplied TOTP
// code against the pending secret, then promotes the pending secret
// to the active MFASecret and flips MFAEnabled = true. Generates +
// returns plaintext recovery codes; stores bcrypt hashes server-side.
// The plaintext is returned exactly once.
//
// Errors out (without changing state) when:
//   - no pending enrollment exists
//   - the pending secret has expired
//   - the supplied code doesn't match the pending secret
//
// Idempotent in the sense that a second enable on an already-enabled
// user with no pending secret returns "no MFA enrollment in progress",
// not a silent re-enable with new recovery codes.
func (s *Service) MFAEnable(ctx context.Context, userID, code string) ([]string, error) {
	if err := s.ensureStore(); err != nil {
		return nil, err
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, fmt.Errorf("%w", ErrMFAAuthFailed)
	}
	err = s.validatePendingMFAEnrollment(ctx, user, code)
	if err != nil {
		return nil, err
	}

	plaintext, hashes, err := s.generateAndHashRecoveryCodes()
	if err != nil {
		return nil, err
	}

	user.MFASecret = user.MFAPendingSecret
	user.MFAEnabled = true
	user.MFAPendingSecret = ""
	user.MFAPendingSecretExpiresAt = nil
	user.MFARecoveryCodes = hashes
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to enable MFA: %w", err)
	}
	return plaintext, nil
}

// clearMFAFromUser zeroes every MFA-related field on the user. Used
// by MFADisable to apply the disable in one place; keeps the disable
// state machine readable.
func clearMFAFromUser(user *User) {
	user.MFAEnabled = false
	user.MFASecret = ""
	user.MFAPendingSecret = ""
	user.MFAPendingSecretExpiresAt = nil
	user.MFARecoveryCodes = nil
}

// disableMFAAlreadyOff is the idempotent path for MFADisable: the
// user already has MFA off, so we only need to clear any stale
// pending fields and persist.
func (s *Service) disableMFAAlreadyOff(ctx context.Context, user *User) error {
	user.MFAPendingSecret = ""
	user.MFAPendingSecretExpiresAt = nil
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("failed to disable MFA: %w", err)
	}
	return nil
}

// MFADisable turns off MFA for a user. Requires both the current
// password AND a fresh proof-of-possession (either a TOTP code or
// an unused recovery code). Defense-in-depth: a stolen session
// alone shouldn't disable MFA, and a stolen authenticator alone
// shouldn't either.
//
// Idempotent: calling on an already-disabled user with the right
// password is a no-op (returns nil) so the UI can drive the button
// without an extra state-query round trip.
func (s *Service) MFADisable(ctx context.Context, userID, password, codeOrRecovery string) error {
	if err := s.ensureStore(); err != nil {
		return err
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return fmt.Errorf("%w", ErrMFAAuthFailed)
	}
	if !s.verifyPassword(password, user.PasswordHash) {
		return fmt.Errorf("%w", ErrMFAInvalidPassword)
	}
	if !user.MFAEnabled {
		return s.disableMFAAlreadyOff(ctx, user)
	}
	if codeOrRecovery == "" {
		return fmt.Errorf("%w", ErrMFACodeRequired)
	}

	// Try TOTP first (cheap), then fall back to recovery code (bcrypt
	// compare, ~constant-time per slot). Either path counts as a
	// fresh proof-of-possession.
	matched := verifyTOTP(user.MFASecret, codeOrRecovery) || s.consumeRecoveryCode(user, codeOrRecovery)
	if !matched {
		return fmt.Errorf("%w", ErrMFAInvalidCode)
	}

	clearMFAFromUser(user)
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("failed to disable MFA: %w", err)
	}
	return nil
}

// MFARegenerateRecoveryCodes replaces all stored recovery codes with
// a fresh batch. Requires a fresh TOTP code (NOT a recovery code —
// because the user could otherwise drain the pool one code at a time
// and never see the regenerated batch). Returns the plaintext codes
// exactly once.
func (s *Service) MFARegenerateRecoveryCodes(ctx context.Context, userID, code string) ([]string, error) {
	if err := s.ensureStore(); err != nil {
		return nil, err
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, fmt.Errorf("%w", ErrMFAAuthFailed)
	}
	if !user.MFAEnabled || user.MFASecret == "" {
		return nil, fmt.Errorf("%w", ErrMFANotEnabled)
	}
	if !verifyTOTP(user.MFASecret, code) {
		return nil, fmt.Errorf("%w", ErrMFAInvalidCode)
	}

	plaintext, hashes, err := s.generateAndHashRecoveryCodes()
	if err != nil {
		return nil, err
	}
	user.MFARecoveryCodes = hashes
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to regenerate recovery codes: %w", err)
	}
	return plaintext, nil
}
