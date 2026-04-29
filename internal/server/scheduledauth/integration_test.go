package scheduledauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestIntegration_FullHTTPRoundtrip drives the validator through an
// actual HTTP server with a mocked JWKS endpoint, exercising the same
// path the running container uses: middleware in front of an inner
// handler, JWKS fetched lazily, JWT verified end-to-end.
//
// The test is split into table-driven sub-cases so each scenario gets
// an independent verdict in `go test` output. It reuses the
// helpers (newTestKey, signToken, jwks, newJWKSServer, baseClaims) from
// validator_test.go since both files share the same test package.
func TestIntegration_FullHTTPRoundtrip(t *testing.T) {
	// Cloud Scheduler uses one signing key per Google identity; rotate
	// is handled by refresh-on-unknown-kid and covered in unit tests.
	signingKey := newTestKey(t, "kid-prod-2026")
	otherKey := newTestKey(t, "kid-attacker")

	// JWKS server publishes the prod key only. otherKey is not in the
	// JWKS, so tokens signed by it must fail.
	jwksSrv := newJWKSServer(t, jwks(signingKey))

	v, err := New(Config{
		Mode:      ModeOIDC,
		Issuer:    "https://accounts.example.com",
		JWKSURL:   jwksSrv.URL,
		Audiences: []string{"https://cudly.example.com"},
		Subjects:  []string{"scheduler@cudly.iam.gserviceaccount.com"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Inner handler the middleware fronts. Returns 200 + body so we can
	// confirm the request reached the handler in the success case.
	const handlerBody = "scheduled-task-ran"
	handlerCalls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(handlerBody))
	})

	// Wire validator middleware + inner handler behind a real HTTP
	// server so we exercise net/http header parsing, query routing,
	// and the actual JWKS fetch over the wire.
	srv := httptest.NewServer(v.Middleware(inner))
	t.Cleanup(srv.Close)

	type tc struct {
		name           string
		buildAuth      func(t *testing.T) string
		wantStatus     int
		wantHandlerHit bool
	}

	now := time.Now()
	cases := []tc{
		{
			name: "valid token signed by published key",
			buildAuth: func(t *testing.T) string {
				return "Bearer " + signToken(t, signingKey, baseClaims(now,
					"scheduler@cudly.iam.gserviceaccount.com",
					"https://cudly.example.com",
					"https://accounts.example.com"))
			},
			wantStatus:     http.StatusOK,
			wantHandlerHit: true,
		},
		{
			name: "token signed by key not in JWKS",
			buildAuth: func(t *testing.T) string {
				return "Bearer " + signToken(t, otherKey, baseClaims(now,
					"scheduler@cudly.iam.gserviceaccount.com",
					"https://cudly.example.com",
					"https://accounts.example.com"))
			},
			wantStatus:     http.StatusUnauthorized,
			wantHandlerHit: false,
		},
		{
			name:           "missing Authorization header",
			buildAuth:      func(t *testing.T) string { return "" },
			wantStatus:     http.StatusUnauthorized,
			wantHandlerHit: false,
		},
		{
			name: "wrong subject (audience matches but sub does not)",
			buildAuth: func(t *testing.T) string {
				return "Bearer " + signToken(t, signingKey, baseClaims(now,
					"random@cudly.iam.gserviceaccount.com",
					"https://cudly.example.com",
					"https://accounts.example.com"))
			},
			wantStatus:     http.StatusUnauthorized,
			wantHandlerHit: false,
		},
		{
			name: "wrong audience (sub matches but aud does not)",
			buildAuth: func(t *testing.T) string {
				return "Bearer " + signToken(t, signingKey, baseClaims(now,
					"scheduler@cudly.iam.gserviceaccount.com",
					"https://attacker.example.com",
					"https://accounts.example.com"))
			},
			wantStatus:     http.StatusUnauthorized,
			wantHandlerHit: false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			callsBefore := handlerCalls

			req, err := http.NewRequestWithContext(context.Background(),
				http.MethodPost, srv.URL+"/api/scheduled/recommendations", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if authz := tt.buildAuth(t); authz != "" {
				req.Header.Set("Authorization", authz)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("client Do: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d (want %d), body=%q", resp.StatusCode, tt.wantStatus, string(body))
			}
			gotHit := handlerCalls > callsBefore
			if gotHit != tt.wantHandlerHit {
				t.Fatalf("handler hit = %v, want %v", gotHit, tt.wantHandlerHit)
			}
			if tt.wantHandlerHit && string(body) != handlerBody {
				t.Fatalf("body = %q, want %q", string(body), handlerBody)
			}
		})
	}
}
