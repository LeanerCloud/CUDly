package scheduledauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Mode is the authentication mode used for scheduled-task requests.
type Mode string

const (
	// ModeOIDC verifies a Google-issued OIDC ID token (RS256 only) on
	// the Authorization header. Subject pinning is required.
	ModeOIDC Mode = "oidc"

	// ModeBearer compares the Authorization header to a shared secret
	// using crypto/subtle.ConstantTimeCompare. Used on Azure where Logic
	// Apps fetch the secret from Key Vault at runtime.
	ModeBearer Mode = "bearer"

	// ModeDisabled disables auth entirely — every request is allowed
	// through. Intended for local development only. The validator logs
	// a WARN at startup and on every request to make this loud.
	ModeDisabled Mode = "disabled"
)

// DefaultClockSkew is the tolerance applied to exp/nbf checks. Matches
// the leeway most OIDC providers (Microsoft, Auth0) recommend.
const DefaultClockSkew = 60 * time.Second

// GoogleIssuer is the canonical OIDC issuer URL for Google-signed ID
// tokens (Cloud Scheduler, Cloud Functions, etc.).
const GoogleIssuer = "https://accounts.google.com"

// GoogleJWKSURL is Google's JWKS endpoint. Used as the default when the
// caller does not override it via SCHEDULED_TASK_OIDC_JWKS_URL.
const GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

// Config holds the parsed configuration for a Validator. NewValidator
// applies defaults and rejects invalid combinations.
type Config struct {
	Mode      Mode
	Issuer    string   // OIDC issuer; defaults to GoogleIssuer in oidc mode
	JWKSURL   string   // OIDC JWKS endpoint; defaults to GoogleJWKSURL in oidc mode
	Audiences []string // accepted aud claims (must be non-empty in oidc mode)
	Subjects  []string // accepted sub claims (REQUIRED non-empty in oidc mode — defence in depth)
	Skew      time.Duration
	Bearer    string // shared secret for bearer mode (must be non-empty)
}

// Validator authenticates inbound requests against the configured mode.
type Validator struct {
	mode      Mode
	verifier  *oidc.IDTokenVerifier // nil unless mode == ModeOIDC
	keySet    *oidc.RemoteKeySet    // nil unless mode == ModeOIDC; powered by go-oidc's single-flight cache
	jwksURL   string                // remembered for Warmup; empty unless mode == ModeOIDC
	audiences map[string]struct{}
	subjects  map[string]struct{}
	skew      time.Duration
	bearer    []byte
	now       func() time.Time // pluggable for tests
}

// claims captures the timestamp claims we need beyond what go-oidc's
// IDToken exposes directly. Issuer/Subject/Audience are read off the
// IDToken value returned by Verify; we only re-parse the payload to
// pick up exp/iat/nbf as int64 timestamps so we can apply our own
// skew-tolerant checks (go-oidc's built-in expiry check has no skew).
type claims struct {
	ExpiresAt int64 `json:"exp"`
	IssuedAt  int64 `json:"iat"`
	NotBefore int64 `json:"nbf"`
}

// New builds a Validator from a parsed Config. The caller is responsible
// for invoking NewFromEnv (or a similar config loader) to populate
// Config from environment variables.
//
// In oidc mode, the underlying *oidc.RemoteKeySet performs JWKS retrieval
// lazily on the first request and refreshes on unknown kid (per
// https://openid.net/specs/openid-connect-core-1_0.html#RotateSigKeys).
// Single-flight is built in. There is no fixed TTL — refresh-on-
// unknown-kid is sufficient because providers always sign new tokens
// with the new kid before retiring the old key.
//
// To pre-warm the cache and surface JWKS endpoint misconfiguration at
// startup, callers should invoke (*Validator).Warmup. Failure is logged
// but does not abort startup — Google's CDN sometimes hiccups and we
// prefer the validator come up with a stale-on-error fetch on the first
// real request rather than crashloop the container.
func New(cfg Config) (*Validator, error) {
	if cfg.Skew <= 0 {
		cfg.Skew = DefaultClockSkew
	}

	v := &Validator{
		mode: cfg.Mode,
		skew: cfg.Skew,
		now:  time.Now,
	}

	switch cfg.Mode {
	case ModeOIDC:
		return configureOIDC(v, cfg)
	case ModeBearer:
		return configureBearer(v, cfg)
	case ModeDisabled:
		// No setup; every request passes (with a per-request WARN log).
		log.Printf("scheduledauth: WARN — auth mode is disabled; /api/scheduled/* is unauthenticated. " +
			"Set SCHEDULED_TASK_AUTH_MODE=oidc (GCP) or =bearer (Azure) in production.")
		return v, nil
	default:
		return nil, fmt.Errorf("%w: unknown mode %q (expected oidc, bearer, or disabled)", ErrConfigInvalid, cfg.Mode)
	}
}

// configureOIDC validates OIDC config, builds set-lookups, and wires the
// go-oidc RemoteKeySet + IDTokenVerifier. Split out of New to keep the
// per-mode setup below the cyclomatic-complexity gate.
func configureOIDC(v *Validator, cfg Config) (*Validator, error) {
	if cfg.Issuer == "" {
		cfg.Issuer = GoogleIssuer
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = GoogleJWKSURL
	}
	if err := validateAbsoluteURL(cfg.JWKSURL, "SCHEDULED_TASK_OIDC_JWKS_URL"); err != nil {
		return nil, err
	}
	if len(cfg.Audiences) == 0 {
		return nil, fmt.Errorf("%w: oidc mode requires SCHEDULED_TASK_OIDC_AUDIENCE", ErrConfigInvalid)
	}
	// Subject pinning is REQUIRED in oidc mode. Any GCP service account
	// in the same org can mint OIDC tokens with arbitrary `aud` values
	// — we MUST also pin the issuer SA unique_id via `sub`. Google uses
	// the service-account unique_id, not the email address, as the `sub`
	// claim for Cloud Scheduler-signed ID tokens. That is what
	// SCHEDULED_TASK_OIDC_SUBJECTS must contain.
	if len(cfg.Subjects) == 0 {
		return nil, fmt.Errorf("%w: oidc mode requires SCHEDULED_TASK_OIDC_SUBJECTS (defence in depth)", ErrConfigInvalid)
	}

	auds, err := cleanSet(cfg.Audiences, "SCHEDULED_TASK_OIDC_AUDIENCE")
	if err != nil {
		return nil, err
	}
	subs, err := cleanSet(cfg.Subjects, "SCHEDULED_TASK_OIDC_SUBJECTS")
	if err != nil {
		return nil, err
	}

	v.audiences = auds
	v.subjects = subs
	v.jwksURL = cfg.JWKSURL
	v.keySet = oidc.NewRemoteKeySet(context.Background(), cfg.JWKSURL)
	v.verifier = oidc.NewVerifier(cfg.Issuer, v.keySet, &oidc.Config{
		// Pin to RS256. Google's tokens are RS256; rejecting anything
		// else closes the alg=none / alg=HS256 confusion family.
		SupportedSigningAlgs: []string{string(oidc.RS256)},
		// We validate audience manually (multiple values supported)
		// and expiry manually (skew tolerance). Issuer is checked by
		// go-oidc.
		SkipClientIDCheck: true,
		SkipExpiryCheck:   true,
	})
	return v, nil
}

func configureBearer(v *Validator, cfg Config) (*Validator, error) {
	if cfg.Bearer == "" {
		return nil, fmt.Errorf("%w: bearer mode requires SCHEDULED_TASK_SECRET", ErrConfigInvalid)
	}
	v.bearer = []byte("Bearer " + cfg.Bearer)
	return v, nil
}

// cleanSet trims and deduplicates input, rejecting blank entries that
// would silently match a missing claim.
func cleanSet(in []string, label string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("%w: %s contains empty value", ErrConfigInvalid, label)
		}
		out[s] = struct{}{}
	}
	return out, nil
}

func validateAbsoluteURL(raw, label string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s must be an absolute URL: %v", ErrConfigInvalid, label, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%w: %s must be an absolute URL", ErrConfigInvalid, label)
	}
	return nil
}

// Mode returns the validator's auth mode. Useful for /health.
func (v *Validator) Mode() Mode {
	return v.mode
}

// warmupTimeout is the fallback deadline for the JWKS warmup probe
// when the caller passes a context without one. http.DefaultClient
// has no timeout, so without this guard a misconfigured / unreachable
// JWKS endpoint would block startup indefinitely.
const warmupTimeout = 5 * time.Second

// Warmup performs a best-effort sanity check on the JWKS endpoint at
// startup. Failure is non-fatal — it is logged and the validator stays
// operable; the underlying *oidc.RemoteKeySet retries on the first real
// request. This avoids crashlooping the container when Google's CDN
// hiccups, while still surfacing misconfiguration (wrong URL, blocked
// egress) prominently in the startup logs.
//
// If ctx has no deadline, a 5s fallback is applied — startup must
// always make forward progress, even if the caller forgot to bound the
// warmup. Callers that DO want to wait longer are respected.
//
// Implementation note: go-oidc's RemoteKeySet does not expose a public
// "fetch now" method — it only fetches on demand from VerifySignature
// (which requires a parseable JWT). We issue an independent HTTP GET
// to the JWKS URL to keep the warmup probe lightweight and reusable.
func (v *Validator) Warmup(ctx context.Context) {
	if v.mode != ModeOIDC || v.jwksURL == "" {
		return
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, warmupTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		log.Printf("scheduledauth: WARN — JWKS warmup request build failed: %v", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("scheduledauth: WARN — JWKS warmup fetch failed for %s: %v "+
			"(validator will retry on first request)", v.jwksURL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("scheduledauth: WARN — JWKS warmup got %d from %s "+
			"(validator will retry on first request)", resp.StatusCode, v.jwksURL)
		return
	}
	if err := validateJWKSBody(resp.Body); err != nil {
		log.Printf("scheduledauth: WARN — JWKS warmup got invalid JWKS payload from %s: %v "+
			"(validator will retry on first request)", v.jwksURL, err)
	}
}

func validateJWKSBody(r io.Reader) error {
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return err
	}
	if doc.Keys == nil {
		return errors.New(`missing "keys" array`)
	}
	return nil
}

// Middleware wraps next with auth enforcement. On failure, writes a 401
// with no body (avoiding any implementation-detail leakage) and logs the
// reason. Successful requests pass through unmodified.
func (v *Validator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v.mode == ModeDisabled {
			log.Printf("scheduledauth: WARN — request to %s allowed without auth (mode=disabled)", r.URL.Path)
			next.ServeHTTP(w, r)
			return
		}

		authz := r.Header.Get("Authorization")
		if err := v.Validate(r.Context(), authz); err != nil {
			log.Printf("scheduledauth: rejected %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Validate is the unit-testable core. It returns nil iff the
// Authorization header satisfies the configured mode's requirements.
func (v *Validator) Validate(ctx context.Context, authz string) error {
	switch v.mode {
	case ModeDisabled:
		return nil
	case ModeBearer:
		return v.validateBearer(authz)
	case ModeOIDC:
		return v.validateOIDC(ctx, authz)
	default:
		// Should be impossible — New rejects unknown modes.
		return fmt.Errorf("%w: misconfigured validator", ErrUnauthorized)
	}
}

func (v *Validator) validateBearer(authz string) error {
	if authz == "" {
		return fmt.Errorf("%w: missing Authorization header", ErrUnauthorized)
	}
	if subtle.ConstantTimeCompare([]byte(authz), v.bearer) != 1 {
		return fmt.Errorf("%w: bearer secret mismatch", ErrUnauthorized)
	}
	return nil
}

func (v *Validator) validateOIDC(ctx context.Context, authz string) error {
	rawToken, err := extractBearerToken(authz)
	if err != nil {
		return err
	}

	// Verify signature, issuer, and algorithm via go-oidc. SkipClientIDCheck
	// and SkipExpiryCheck are enabled because we apply our own multi-aud
	// and skew-tolerant expiry checks below.
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}

	// Re-parse the payload to access iat / nbf — IDToken exposes Expiry
	// and IssuedAt but go-oidc's expiry check has no skew tolerance, and
	// nbf is not exposed as a typed field.
	var c claims
	if err := idToken.Claims(&c); err != nil {
		return fmt.Errorf("%w: malformed claims: %v", ErrUnauthorized, err)
	}

	if err := v.checkTimestamps(c); err != nil {
		return err
	}

	// Audience: at least one of the token's aud values must be in our
	// allow-list. The token typically has exactly one audience but the
	// spec permits multiple.
	if !audienceMatches(idToken.Audience, v.audiences) {
		return fmt.Errorf("%w: audience %v not in allowlist", ErrUnauthorized, idToken.Audience)
	}

	// Subject pinning: required defence-in-depth — any GCP SA in the
	// org could mint a token with our `aud` value, but only the
	// scheduler SA has our `sub`.
	if _, ok := v.subjects[idToken.Subject]; !ok {
		return fmt.Errorf("%w: subject %q not in allowlist", ErrUnauthorized, idToken.Subject)
	}

	log.Printf("scheduledauth: oidc token accepted (sub=%s, aud=%v)", idToken.Subject, idToken.Audience)
	return nil
}

// extractBearerToken pulls the JWT out of an `Authorization: Bearer <jwt>`
// header. Returns ErrUnauthorized for any shape that isn't a non-empty
// bearer token.
func extractBearerToken(authz string) (string, error) {
	if authz == "" {
		return "", fmt.Errorf("%w: missing Authorization header", ErrUnauthorized)
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		return "", fmt.Errorf("%w: Authorization is not a Bearer token", ErrUnauthorized)
	}
	raw := strings.TrimSpace(authz[len(prefix):])
	if raw == "" {
		return "", fmt.Errorf("%w: empty bearer token", ErrUnauthorized)
	}
	return raw, nil
}

// checkTimestamps applies skew-tolerant exp/nbf/iat checks. exp is
// required (Cloud Scheduler always sets it); nbf and iat are optional
// per RFC 7519. The iat-in-future check defends against horizontally-
// spread clock-skew attacks.
func (v *Validator) checkTimestamps(c claims) error {
	now := v.now()

	if c.ExpiresAt == 0 {
		return fmt.Errorf("%w: missing exp claim", ErrUnauthorized)
	}
	exp := time.Unix(c.ExpiresAt, 0)
	if now.After(exp.Add(v.skew)) {
		return fmt.Errorf("%w: token expired at %s (now=%s, skew=%s)",
			ErrUnauthorized, exp.Format(time.RFC3339), now.Format(time.RFC3339), v.skew)
	}

	if c.NotBefore != 0 {
		nbf := time.Unix(c.NotBefore, 0)
		if now.Add(v.skew).Before(nbf) {
			return fmt.Errorf("%w: token not yet valid (nbf=%s, now=%s)",
				ErrUnauthorized, nbf.Format(time.RFC3339), now.Format(time.RFC3339))
		}
	}

	if c.IssuedAt != 0 {
		iat := time.Unix(c.IssuedAt, 0)
		if iat.After(now.Add(v.skew)) {
			return fmt.Errorf("%w: token iat in the future (iat=%s, now=%s)",
				ErrUnauthorized, iat.Format(time.RFC3339), now.Format(time.RFC3339))
		}
	}
	return nil
}

func audienceMatches(tokenAud []string, allowed map[string]struct{}) bool {
	for _, a := range tokenAud {
		if _, ok := allowed[a]; ok {
			return true
		}
	}
	return false
}

// IsUnauthorized reports whether err originated from a Validate failure.
// Useful for callers that need to distinguish auth failures from config
// errors.
func IsUnauthorized(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}
