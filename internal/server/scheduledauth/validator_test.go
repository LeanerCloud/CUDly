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
	"runtime"
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
	// 1. Server starts with key A. Token signed by A validates.
	// 2. Provider rotates: server now publishes key B. Token signed by B
	//    arrives with a previously-unseen kid. The validator MUST refresh
	//    the JWKS and accept the new token.
	//
	// The verifyWithRotationRetry path in validateOIDC makes this
	// deterministic: when the cached (or in-flight) JWKS lacks kid-B, the
	// first Verify returns a signature error, the verifier is rebuilt with a
	// fresh RemoteKeySet, and the retry fetches the rotated JWKS. No goroutine
	// timing is relied upon - see also TestValidate_OIDC_KeyRotation_StaleInflight
	// for a direct exercise of the stale-inflight path.
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
	// /swap handler 5xx's (or returns non-200 due to a body short-read),
	// the JWKS would silently NOT update and the test would fail later at
	// "unknown kid" instead of pointing at the real cause.
	jwksB := jwks(keyB)
	swap, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/swap", bytes.NewReader(jwksB))
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

func TestValidate_OIDC_KeyRotation_StaleInflight(t *testing.T) {
	// Directly exercise the stale-inflight race diagnosed in issue #1381.
	//
	// go-oidc's RemoteKeySet uses a single goroutine ("inflight") to fetch
	// JWKS. The goroutine calls inflight.done(keys) to unblock callers, then
	// must re-acquire the mutex to set inflight=nil. In the window between
	// done() and inflight=nil, a new caller joins the same inflight and
	// receives the pre-rotation keys, causing a spurious signature failure.
	//
	// Setup: a JWKS server that holds its first response (simulating the
	// inflight window). While the first fetch is blocked, the JWKS rotates
	// to key B. A second validation (tokB, signed with key B) joins the
	// blocked inflight and will receive stale key-A keys.
	//
	// Pre-fix behavior: tokB fails with "failed to verify id token signature".
	// Post-fix behavior: the retry rebuilds the RemoteKeySet (fresh, no stale
	// inflight), fetches the rotated JWKS, and verifies tokB successfully.
	keyA := newTestKey(t, "kid-A")
	keyB := newTestKey(t, "kid-B")

	var (
		fetchCount   atomic.Int64
		firstArrived = make(chan struct{})
		release      = make(chan struct{})
		jwksA        = jwks(keyA)
		jwksB        = jwks(keyB)
	)

	// The JWKS server is deterministic by request number:
	//   request 1: serves keyA JWKS (held until release) - simulates the
	//              inflight completing with pre-rotation keys
	//   request 2+: serves keyB JWKS - the post-rotation state that the
	//               retry must pick up
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		n := fetchCount.Add(1)
		var body []byte
		if n == 1 {
			close(firstArrived) // signal: first JWKS request is in progress
			<-release           // hold until the test releases (stale-inflight window)
			body = jwksA
		} else {
			body = jwksB
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(body)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	v := newOIDCValidator(t, ts.URL)

	tokA := signToken(t, keyA, baseClaims(time.Now(),
		testSchedulerSubject, "https://api.example.com", "https://accounts.example.com"))
	tokB := signToken(t, keyB, baseClaims(time.Now(),
		testSchedulerSubject, "https://api.example.com", "https://accounts.example.com"))

	// Start tokA validation in background; it triggers the first (held) JWKS fetch.
	errA := make(chan error, 1)
	go func() { errA <- v.Validate(context.Background(), "Bearer "+tokA) }()

	// Wait for the first JWKS fetch to start (inflight#1 is now active).
	<-firstArrived

	// Start tokB validation while inflight#1 is blocked. go-oidc deduplicates
	// concurrent fetches via single-flight, so tokB joins the same inflight
	// and will receive key-A keys when the inflight is released.
	// NOTE: the inflight join is best-effort (Gosched below, no hard sync
	// hook into go-oidc internals) - this test exercises the retry only when
	// the join wins; either way tokB must succeed. The error-string contract
	// the retry depends on is pinned deterministically by
	// TestIsSignatureError_MatchesGoOIDCContract.
	errB := make(chan error, 1)
	go func() { errB <- v.Validate(context.Background(), "Bearer "+tokB) }()

	// Yield the scheduler a few times to let the tokB goroutine progress
	// into keysFromRemote and join the active inflight before we release it.
	for i := 0; i < 20; i++ {
		runtime.Gosched()
	}

	// Release the first fetch. The inflight returns key-A keys.
	// tokA verifies (kid-A present). tokB fails (kid-B absent in key-A set)
	// and the validator's retry rebuilds the RemoteKeySet, fetches the
	// rotated JWKS (request 2, returns key-B), and accepts tokB.
	close(release)

	if err := <-errA; err != nil {
		t.Errorf("tokA: expected success, got %v", err)
	}
	if err := <-errB; err != nil {
		t.Errorf("tokB (stale-inflight retry): expected success, got %v", err)
	}
}

func TestVerifyRetry_RebuildRateLimited(t *testing.T) {
	// Amplification-DoS guard: sequential bad-signature tokens must trigger
	// at most ONE verifier rebuild per minRebuildInterval. Without the rate
	// limit, every garbage token would rebuild the keyset and fire an extra
	// outbound JWKS GET on the retry.
	//
	// The invariant under test: after two back-to-back bad-signature tokens
	// with a frozen clock (so the second is always within minRebuildInterval
	// of the first), exactly one rebuild happens. We assert this by
	// inspecting v.verifier (pointer equality) and v.lastRebuild under
	// v.verMu rather than counting raw JWKS fetches.
	//
	// Why pointer equality, not fetch counts: go-oidc v3's keysFromRemote
	// calls inflight.done() before re-acquiring its mutex to update
	// r.cachedKeys and clear r.inflight. If the next Verify call lands in
	// that narrow window, it joins the still-live inflight without issuing
	// an HTTP GET, making the total fetch count non-deterministic (2 or 3).
	// The rebuild count is always deterministic: the validator guards it
	// under verMu with an explicit rate-limit check.
	keyA := newTestKey(t, "kid-A")
	keyX := newTestKey(t, "kid-X") // never published
	srv := newJWKSServer(t, jwks(keyA))
	v := newOIDCValidator(t, srv.URL)

	// Freeze the clock so the second token is deterministically within
	// minRebuildInterval of the first rebuild. No time.Sleep needed.
	t0 := time.Now()
	v.now = func() time.Time { return t0 }

	// Snapshot the verifier pointer before any token is presented.
	v.verMu.RLock()
	origVer := v.verifier
	v.verMu.RUnlock()

	// Token 1: triggers the initial rebuild (lastRebuild is zero, so the
	// rate-limit condition does not fire).
	tok1 := signToken(t, keyX, baseClaims(t0,
		testSchedulerSubject, "https://api.example.com", "https://accounts.example.com"))
	if err := v.Validate(context.Background(), "Bearer "+tok1); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token 1: expected ErrUnauthorized, got: %v", err)
	}

	v.verMu.RLock()
	ver1 := v.verifier
	rb1 := v.lastRebuild
	v.verMu.RUnlock()

	if ver1 == origVer {
		t.Fatal("expected verifier to be rebuilt after first bad-signature token")
	}
	if !rb1.Equal(t0) {
		t.Fatalf("lastRebuild should equal frozen t0 after first rebuild, got %v", rb1)
	}

	// Token 2: within minRebuildInterval (clock still frozen at t0), so the
	// rate limiter must suppress the rebuild entirely.
	tok2 := signToken(t, keyX, baseClaims(t0,
		testSchedulerSubject, "https://api.example.com", "https://accounts.example.com"))
	if err := v.Validate(context.Background(), "Bearer "+tok2); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token 2: expected ErrUnauthorized, got: %v", err)
	}

	v.verMu.RLock()
	ver2 := v.verifier
	rb2 := v.lastRebuild
	v.verMu.RUnlock()

	if ver2 != ver1 {
		t.Fatal("rate limiter must prevent a second rebuild within minRebuildInterval: verifier pointer changed")
	}
	if !rb2.Equal(t0) {
		t.Fatalf("lastRebuild must not change on rate-limited token, got %v", rb2)
	}

	// Sanity: at least two JWKS fetches must have occurred (the lazy initial
	// fetch and the rebuild retry). The exact count is non-deterministic due
	// to go-oidc's goroutine scheduling (see comment above), so we only
	// assert a lower bound.
	if hits := srv.hits.Load(); hits < 2 {
		t.Fatalf("expected at least 2 JWKS fetches, got %d", hits)
	}
}

func TestVerifyRetry_RotationRecoversAfterInterval(t *testing.T) {
	// The rebuild rate limit must not permanently wedge the validator: once
	// minRebuildInterval elapses, a genuine key rotation must be recoverable
	// and subsequent Validate calls must succeed.
	//
	// JWKS server design: serves keyA until serveKeyB is flipped atomically
	// between the two Validate calls, then serves keyB for all subsequent
	// fetches. This is more deterministic than the previous n>=4 gate:
	//   - n>=4 assumed exactly 3 HTTP fetches before the second Validate's
	//     rebuild retry, but go-oidc v3's keysFromRemote goroutine calls
	//     inflight.done() before re-acquiring its mutex to clear r.inflight.
	//     A concurrent Verify joining that still-live inflight skips an HTTP
	//     GET, making the ordinal of subsequent fetches non-deterministic.
	//   - The serveKeyB flag avoids fetch-count assumptions: every fetch
	//     during the second Validate (whether via go-oidc's own
	//     refresh-on-unknown-kid or the validator's rebuild retry) reliably
	//     receives keyB.
	//
	// The behavioral invariant under test: the second Validate MUST succeed
	// after the interval elapses. Recovery may happen via go-oidc's own
	// refresh-on-unknown-kid (when the cached keys and the inflight both
	// return keyA and the library retries with the fresh keyB response)
	// OR via the validator's rebuild path (when a sig error propagates up
	// and the interval has elapsed). Both are valid. We assert the
	// behavioral outcome, not the internal path.
	keyA := newTestKey(t, "kid-A")
	keyB := newTestKey(t, "kid-B")
	jwksA := jwks(keyA)
	jwksB := jwks(keyB)

	var (
		fetchCount atomic.Int64
		serveKeyB  atomic.Bool // flipped between the two Validate calls
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		body := jwksA
		if serveKeyB.Load() {
			body = jwksB
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(body)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	v := newOIDCValidator(t, ts.URL)

	// Injectable clock: start at t0, then jump past minRebuildInterval.
	// No time.Sleep required.
	t0 := time.Now()
	current := t0
	v.now = func() time.Time { return current }

	// Snapshot verifier before first Validate.
	v.verMu.RLock()
	ver0 := v.verifier
	v.verMu.RUnlock()

	tokB := signToken(t, keyB, baseClaims(t0,
		testSchedulerSubject, "https://api.example.com", "https://accounts.example.com"))

	// First Validate at t0: JWKS serves keyA, tokB (signed with keyB) must
	// fail. Rebuild#1 fires (lastRebuild is zero so rate-limit permits it),
	// but the retry also fetches keyA, so the result is still 401.
	if err := v.Validate(context.Background(), "Bearer "+tokB); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("t0: expected ErrUnauthorized while JWKS is stale, got: %v", err)
	}

	// Rebuild#1 must have fired: verifier pointer must have changed.
	v.verMu.RLock()
	ver1 := v.verifier
	v.verMu.RUnlock()
	if ver1 == ver0 {
		t.Fatal("expected rebuild#1 after first Validate fails with stale JWKS")
	}

	// Advance the clock past minRebuildInterval AND flip the JWKS gate so
	// all subsequent fetches return keyB. Both mutations happen in the test
	// goroutine before the second Validate call; there is no concurrent
	// reader of either variable at this point.
	current = t0.Add(minRebuildInterval + time.Second)
	serveKeyB.Store(true)

	// Second Validate at t0+11s: the rate-limit interval has elapsed, so
	// the validator is free to rebuild if needed. Whether recovery happens
	// via go-oidc's own refresh-on-unknown-kid (finding keyB in the remote
	// fetch) or via the validator's rebuild path, the call MUST succeed.
	if err := v.Validate(context.Background(), "Bearer "+tokB); err != nil {
		t.Fatalf("post-interval: expected rotation to recover via rebuild, got: %v", err)
	}

	// Sanity: at least two fetches must have occurred (one for the first
	// Validate, one or more for the second). Exact count is non-deterministic
	// due to go-oidc's goroutine scheduling (see comment above).
	if hits := fetchCount.Load(); hits < 2 {
		t.Fatalf("expected at least 2 JWKS fetches, got %d", hits)
	}
}

func TestIsSignatureError_MatchesGoOIDCContract(t *testing.T) {
	// Guard: the retry path keys off go-oidc's literal error string
	// "failed to verify signature" (verify.go in go-oidc/v3). If a future
	// go-oidc bump rewords it, this test fails loudly instead of the retry
	// being silently disabled. It drives REAL tokens through the actual
	// verifier rather than asserting against a hand-built error.
	key := newTestKey(t, "kid-1")
	imposter := newTestKey(t, "kid-1") // same kid, different key -> pure signature failure
	srv := newJWKSServer(t, jwks(key))
	v := newOIDCValidator(t, srv.URL)

	badSig := signToken(t, imposter, baseClaims(time.Now(),
		testSchedulerSubject, "https://api.example.com", "https://accounts.example.com"))
	_, err := v.verifier.Verify(context.Background(), badSig)
	if err == nil {
		t.Fatalf("expected signature verification to fail")
	}
	if !isSignatureError(err) {
		t.Fatalf("isSignatureError = false for a real go-oidc signature failure: %v\n"+
			"go-oidc likely reworded its error string - update isSignatureError to match", err)
	}

	// Negative: a claim failure (wrong issuer) must NOT be classified as a
	// signature error, otherwise iss failures would start triggering retries.
	wrongIss := signToken(t, key, baseClaims(time.Now(),
		testSchedulerSubject, "https://api.example.com", "https://attacker-iss.example.com"))
	_, err = v.verifier.Verify(context.Background(), wrongIss)
	if err == nil {
		t.Fatalf("expected issuer verification to fail")
	}
	if isSignatureError(err) {
		t.Fatalf("isSignatureError = true for an issuer failure; retry must not fire on claim errors: %v", err)
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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/scheduled/foo", nil)
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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/scheduled/foo", nil)
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

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/scheduled/foo", nil)
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
