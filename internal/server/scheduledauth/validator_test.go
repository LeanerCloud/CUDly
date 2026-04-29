package scheduledauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// testSchedulerSubject is the SA unique_id format Google puts in the
// JWT `sub` claim for SA-signed ID tokens (21-digit numeric). The
// production SCHEDULED_TASK_OIDC_SUBJECTS expects this format, NOT the
// SA email — wiring tests around the email shape would let a "sub-as-
// email" regression slip past the suite.
const testSchedulerSubject = "112233445566778899001"

// testNonSchedulerSubject is a stand-in for any non-listed SA's unique_id,
// used by negative-path tests that verify the validator rejects subjects
// outside SCHEDULED_TASK_OIDC_SUBJECTS. Same shape as testSchedulerSubject
// (21-digit numeric) — using an email-shaped fixture in the rejection
// tests would have made them pass for the wrong reason (subject
// mismatch by FORMAT, not by VALUE), masking a regression that
// silently widened the validator to accept any string.
const testNonSchedulerSubject = "999888777666555444333"

// testKey wraps an RSA keypair plus the kid we'll publish in the JWKS.
type testKey struct {
	priv *rsa.PrivateKey
	pub  *rsa.PublicKey
	kid  string
}

func newTestKey(t *testing.T, kid string) *testKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return &testKey{priv: priv, pub: &priv.PublicKey, kid: kid}
}

// jwks renders a JWKS document containing the given public keys.
func jwks(keys ...*testKey) []byte {
	type jwk struct {
		Kty string `json:"kty"`
		Use string `json:"use"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	out := struct {
		Keys []jwk `json:"keys"`
	}{}
	for _, k := range keys {
		j := jose.JSONWebKey{Key: k.pub, KeyID: k.kid, Algorithm: string(jose.RS256), Use: "sig"}
		raw, _ := j.MarshalJSON()
		var parsed jwk
		_ = json.Unmarshal(raw, &parsed)
		out.Keys = append(out.Keys, parsed)
	}
	b, _ := json.Marshal(out)
	return b
}

// jwksServer returns an httptest.Server that serves a JWKS document at
// "/" and counts how many times it has been hit (atomic, so concurrent
// fetches under the single-flight test see a stable value).
type jwksServer struct {
	*httptest.Server
	hits *atomic.Int64
}

func newJWKSServer(t *testing.T, body []byte) *jwksServer {
	t.Helper()
	hits := &atomic.Int64{}
	mu := &sync.Mutex{}
	current := body
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(current)
	})
	mux.HandleFunc("/swap", func(w http.ResponseWriter, r *http.Request) {
		// Body of POST /swap replaces the served JWKS. Used to test
		// refresh-on-unknown-kid. io.ReadAll handles short Reads
		// correctly — a single Body.Read call may return less than
		// ContentLength and silently truncate the JWKS.
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		current = buf
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &jwksServer{Server: srv, hits: hits}
}

// signToken signs a JWT with the given key and claim set.
func signToken(t *testing.T, key *testKey, c map[string]interface{}) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key.priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", key.kid),
	)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	tok, err := jwt.Signed(signer).Claims(c).Serialize()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// signTokenAlg signs with a non-RS256 algorithm. Used to confirm the
// validator rejects unexpected algs.
func signTokenAlg(t *testing.T, alg jose.SignatureAlgorithm, key interface{}, kid string, c map[string]interface{}) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: alg, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid),
	)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	tok, err := jwt.Signed(signer).Claims(c).Serialize()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func baseClaims(now time.Time, sub, aud, iss string) map[string]interface{} {
	return map[string]interface{}{
		"iss": iss,
		"sub": sub,
		"aud": aud,
		"exp": now.Add(5 * time.Minute).Unix(),
		"iat": now.Add(-1 * time.Minute).Unix(),
	}
}

func newOIDCValidator(t *testing.T, jwksURL string) *Validator {
	t.Helper()
	v, err := New(Config{
		Mode:      ModeOIDC,
		Issuer:    "https://accounts.example.com",
		JWKSURL:   jwksURL,
		Audiences: []string{"https://api.example.com"},
		Subjects:  []string{testSchedulerSubject},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func TestValidate_OIDC_Valid(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, key, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))

	if err := v.Validate(context.Background(), "Bearer "+tok); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_OIDC_MissingHeader(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	if err := v.Validate(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_NotBearer(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	if err := v.Validate(context.Background(), "Basic Zm9vOmJhcg=="); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_Expired(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	now := time.Now()
	claims := baseClaims(now,
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com")
	// 2 minutes past expiry, well beyond the 60s skew.
	claims["exp"] = now.Add(-2 * time.Minute).Unix()
	tok := signToken(t, key, claims)

	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_ExpiryWithinSkew(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	now := time.Now()
	claims := baseClaims(now,
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com")
	// Just expired but within the 60s skew window.
	claims["exp"] = now.Add(-30 * time.Second).Unix()
	tok := signToken(t, key, claims)

	// Pin time so the test is deterministic.
	v.now = func() time.Time { return now }
	if err := v.Validate(context.Background(), "Bearer "+tok); err != nil {
		t.Fatalf("expected accepted within skew, got: %v", err)
	}
}

func TestValidate_OIDC_WrongAudience(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, key, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://attacker.example.com",
		"https://accounts.example.com"))

	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_WrongIssuer(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, key, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://attacker-iss.com"))

	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_WrongSubject(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, key, baseClaims(time.Now(),
		testNonSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))

	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_BadSignature(t *testing.T) {
	key := newTestKey(t, "kid-1")
	other := newTestKey(t, "kid-1") // same kid, different key — sig won't verify
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, other, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))

	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_AlgorithmConfusion(t *testing.T) {
	// Server publishes only an RS256 key. Client tries to sign an HS256
	// token using the public key as a shared secret (the classic alg
	// confusion attack). The verifier MUST reject this because we
	// pinned SupportedSigningAlgs to ["RS256"].
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	// We can't easily sign an HS256 with an RSA public key via go-jose
	// (it expects []byte), but signing with HS256 + a random secret +
	// kid="kid-1" is a strictly easier case for the attacker; if the
	// validator rejects this it covers the core alg-confusion gap.
	tok := signTokenAlg(t, jose.HS256, []byte("attacker-secret-32-bytes-padding!!"), key.kid, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))

	err := v.Validate(context.Background(), "Bearer "+tok)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for HS256 token, got: %v", err)
	}
}

func TestValidate_OIDC_IATInFuture(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	now := time.Now()
	claims := baseClaims(now,
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com")
	// 2 minutes in the future, well beyond the 60s skew.
	claims["iat"] = now.Add(2 * time.Minute).Unix()
	tok := signToken(t, key, claims)

	v.now = func() time.Time { return now }
	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_NotBeforeFuture(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	now := time.Now()
	claims := baseClaims(now,
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com")
	claims["nbf"] = now.Add(2 * time.Minute).Unix()
	tok := signToken(t, key, claims)

	v.now = func() time.Time { return now }
	if err := v.Validate(context.Background(), "Bearer "+tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_OIDC_AudienceListClaim(t *testing.T) {
	// `aud` may be either a string or a JSON list. Make sure the list
	// form is accepted as long as one entry matches.
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	now := time.Now()
	claims := baseClaims(now,
		testSchedulerSubject,
		"placeholder", // overridden below
		"https://accounts.example.com")
	claims["aud"] = []string{"https://other.example.com", "https://api.example.com"}
	tok := signToken(t, key, claims)

	if err := v.Validate(context.Background(), "Bearer "+tok); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_OIDC_KeyRotation_RefreshOnUnknownKid(t *testing.T) {
	// 1. Server starts with key A. Token signed by A → validates.
	// 2. Provider rotates: server now publishes key B. Token signed by B
	//    arrives with a previously-unseen kid. The validator MUST refresh
	//    the JWKS and accept the new token.
	keyA := newTestKey(t, "kid-A")
	keyB := newTestKey(t, "kid-B")
	srv := newJWKSServer(t, jwks(keyA))
	v := newOIDCValidator(t, srv.URL)

	tokA := signToken(t, keyA, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))
	if err := v.Validate(context.Background(), "Bearer "+tokA); err != nil {
		t.Fatalf("kid A: %v", err)
	}

	// Swap the JWKS to publish kid B.
	//
	// Both the request build and the response status are checked: if the
	// /swap handler 5xx's (or — more subtly — returns a non-200 because
	// the body short-read), the JWKS would silently NOT update. The test
	// would then fail later at "unknown kid" instead of pointing at the
	// real cause. Surfacing the swap failure here keeps the diagnostic
	// chain short.
	jwksB := jwks(keyB)
	swap, err := http.NewRequest(http.MethodPost, srv.URL+"/swap", strings.NewReader(string(jwksB)))
	if err != nil {
		t.Fatalf("build swap request: %v", err)
	}
	swap.ContentLength = int64(len(jwksB))
	resp, err := http.DefaultClient.Do(swap)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("swap: unexpected status %d, body: %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	tokB := signToken(t, keyB, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))
	if err := v.Validate(context.Background(), "Bearer "+tokB); err != nil {
		t.Fatalf("kid B (post-rotation): %v", err)
	}
}

func TestValidate_OIDC_SingleFlight_StampedeProtection(t *testing.T) {
	// 50 concurrent verifications of fresh tokens after a cold start
	// should result in at most a small handful of JWKS fetches (the
	// underlying RemoteKeySet uses single-flight). Without the
	// protection we'd see ~50 hits.
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, key, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := v.Validate(context.Background(), "Bearer "+tok); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent validate: %v", err)
	}
	hits := srv.hits.Load()
	if hits > 5 {
		t.Errorf("expected <=5 JWKS fetches under single-flight, got %d", hits)
	}
}

func TestValidate_Bearer_OK(t *testing.T) {
	v, err := New(Config{Mode: ModeBearer, Bearer: "topsecret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Validate(context.Background(), "Bearer topsecret"); err != nil {
		t.Fatalf("expected ok, got: %v", err)
	}
}

func TestValidate_Bearer_Mismatch(t *testing.T) {
	v, err := New(Config{Mode: ModeBearer, Bearer: "topsecret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Validate(context.Background(), "Bearer wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_Bearer_MissingHeader(t *testing.T) {
	v, err := New(Config{Mode: ModeBearer, Bearer: "topsecret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Validate(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestValidate_Disabled_AlwaysAllows(t *testing.T) {
	v, err := New(Config{Mode: ModeDisabled})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Validate(context.Background(), ""); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestNew_Rejects_OIDCWithoutSubjects(t *testing.T) {
	_, err := New(Config{
		Mode:      ModeOIDC,
		Audiences: []string{"https://api.example.com"},
		// Subjects intentionally empty.
	})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got: %v", err)
	}
}

func TestNew_Rejects_OIDCWithoutAudiences(t *testing.T) {
	_, err := New(Config{
		Mode:     ModeOIDC,
		Subjects: []string{testSchedulerSubject},
	})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got: %v", err)
	}
}

func TestNew_RejectsOIDCMalformedJWKSURL(t *testing.T) {
	for _, jwksURL := range []string{"://bad", "/relative/path", "https://"} {
		t.Run(jwksURL, func(t *testing.T) {
			_, err := New(Config{
				Mode:      ModeOIDC,
				JWKSURL:   jwksURL,
				Audiences: []string{"https://api.example.com"},
				Subjects:  []string{testSchedulerSubject},
			})
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("expected ErrConfigInvalid, got: %v", err)
			}
		})
	}
}

func TestNew_Rejects_BearerWithoutSecret(t *testing.T) {
	_, err := New(Config{Mode: ModeBearer})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got: %v", err)
	}
}

func TestNew_Rejects_UnknownMode(t *testing.T) {
	_, err := New(Config{Mode: Mode("bogus")})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got: %v", err)
	}
}

func TestLoadConfig_RejectsMissingAuthMode(t *testing.T) {
	_, err := LoadConfig(EnvMap{})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid for unset SCHEDULED_TASK_AUTH_MODE, got: %v", err)
	}
}

func TestLoadConfig_ExplicitDisabledIsAccepted(t *testing.T) {
	cfg, err := LoadConfig(EnvMap{EnvAuthMode: "disabled"})
	if err != nil {
		t.Fatalf("LoadConfig(disabled): %v", err)
	}
	if cfg.Mode != ModeDisabled {
		t.Fatalf("mode = %s, want %s", cfg.Mode, ModeDisabled)
	}
}

func TestLoadConfig_OIDCParsesCommaSeparated(t *testing.T) {
	cfg, err := LoadConfig(EnvMap{
		EnvAuthMode:     "oidc",
		EnvOIDCAudience: " a@example.com , , b@example.com ",
		EnvOIDCSubjects: "x@example.com,y@example.com",
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Mode != ModeOIDC {
		t.Fatalf("mode = %s", cfg.Mode)
	}
	if got, want := strings.Join(cfg.Audiences, ","), "a@example.com,b@example.com"; got != want {
		t.Fatalf("audiences = %q, want %q", got, want)
	}
	if got, want := strings.Join(cfg.Subjects, ","), "x@example.com,y@example.com"; got != want {
		t.Fatalf("subjects = %q, want %q", got, want)
	}
}

func TestLoadConfig_RejectsUnknownMode(t *testing.T) {
	_, err := LoadConfig(EnvMap{EnvAuthMode: "bogus"})
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got: %v", err)
	}
}

func TestLoadConfig_BearerParse(t *testing.T) {
	cfg, err := LoadConfig(EnvMap{
		EnvAuthMode:     "bearer",
		EnvBearerSecret: "topsecret",
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Mode != ModeBearer || cfg.Bearer != "topsecret" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestMiddleware_OIDC_RejectsAndLogsButDoesNotCallNext(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	mw := v.Middleware(next)

	req := httptest.NewRequest("POST", "/api/scheduled/foo", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if called {
		t.Fatalf("next handler must not be called on auth failure")
	}
}

func TestMiddleware_OIDC_AllowsAndCallsNextOnSuccess(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	tok := signToken(t, key, baseClaims(time.Now(),
		testSchedulerSubject,
		"https://api.example.com",
		"https://accounts.example.com"))

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := v.Middleware(next)

	req := httptest.NewRequest("POST", "/api/scheduled/foo", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatalf("next handler should be called on success")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestMiddleware_Disabled_PassesThroughWithWarn(t *testing.T) {
	v, err := New(Config{Mode: ModeDisabled})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	mw := v.Middleware(next)

	req := httptest.NewRequest("POST", "/api/scheduled/foo", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if !called {
		t.Fatalf("disabled middleware must call next")
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestWarmup_HitsJWKSEndpoint(t *testing.T) {
	key := newTestKey(t, "kid-1")
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	before := srv.hits.Load()
	v.Warmup(context.Background())
	after := srv.hits.Load()
	if after <= before {
		t.Fatalf("Warmup should have hit JWKS endpoint at least once (before=%d after=%d)", before, after)
	}
}

func TestValidateJWKSBody_RequiresKeysArray(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "valid", body: `{"keys":[]}`},
		{name: "invalid json", body: `{`, wantErr: true},
		{name: "missing keys", body: `{}`, wantErr: true},
		{name: "null keys", body: `{"keys":null}`, wantErr: true},
		{name: "object keys", body: `{"keys":{}}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJWKSBody(strings.NewReader(tt.body))
			if tt.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil, got: %v", err)
			}
		})
	}
}

func TestWarmup_LoggedAndNonFatal_OnDeadEndpoint(t *testing.T) {
	// Capture the global logger so we can assert the failure path was
	// actually exercised. Without this, the test passes just as well if
	// Warmup were to short-circuit before the fetch ever ran (CR pass on
	// PR #161 — "did not hang" alone doesn't pin the non-fatal branch).
	// No tests in this package use t.Parallel(), so swapping the global
	// log writer here is safe; t.Cleanup restores it.
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origOut) })

	v, err := New(Config{
		Mode:      ModeOIDC,
		Issuer:    "https://accounts.example.com",
		JWKSURL:   "http://127.0.0.1:1/this-port-is-closed", // ECONNREFUSED
		Audiences: []string{"https://api.example.com"},
		Subjects:  []string{testSchedulerSubject},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Warmup must not panic or block. Use a short timeout to make the
	// test fast even if Warmup were to misbehave.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	v.Warmup(ctx) // non-fatal — only logs

	// Assert the failure-path log fired. The validator emits
	// "scheduledauth: WARN — JWKS warmup fetch failed for <url>: <err>"
	// on connect refused (validator.go:255). We pin on the substring
	// "JWKS warmup" so a future log-message tweak doesn't break the
	// test for cosmetic reasons, while still proving the code path
	// reached the post-fetch error branch.
	logs := logBuf.String()
	if !strings.Contains(logs, "JWKS warmup") {
		t.Fatalf("expected a JWKS-warmup failure log, got: %q", logs)
	}
}

// guard: sanity-check that Mode is reported back.
func TestMode(t *testing.T) {
	for _, m := range []Mode{ModeOIDC, ModeBearer, ModeDisabled} {
		var v *Validator
		var err error
		switch m {
		case ModeOIDC:
			v, err = New(Config{
				Mode:      ModeOIDC,
				JWKSURL:   "http://127.0.0.1:1",
				Audiences: []string{"a"},
				Subjects:  []string{"s"},
			})
		case ModeBearer:
			v, err = New(Config{Mode: ModeBearer, Bearer: "x"})
		case ModeDisabled:
			v, err = New(Config{Mode: ModeDisabled})
		}
		if err != nil {
			t.Fatalf("New(%s): %v", m, err)
		}
		if v.Mode() != m {
			t.Fatalf("Mode() = %s, want %s", v.Mode(), m)
		}
	}
}

// guard: ensure typed sentinel works with errors.Is.
func TestIsUnauthorized(t *testing.T) {
	wrapped := fmt.Errorf("wrap: %w", ErrUnauthorized)
	if !IsUnauthorized(wrapped) {
		t.Fatalf("IsUnauthorized failed for wrapped sentinel")
	}
	if IsUnauthorized(errors.New("other")) {
		t.Fatalf("IsUnauthorized matched unrelated error")
	}
}
