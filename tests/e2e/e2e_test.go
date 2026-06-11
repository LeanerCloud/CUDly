//go:build e2e

// Package e2e contains black-box end-to-end tests that exercise a running
// CUDly HTTP server over the network. They are driven by docker-compose
// (docker-compose.test.yml, profile "test"): the test-runner container sets
// API_URL to the cudly-app service and runs `go test -tags=e2e ./...`.
//
// The suite intentionally uses only the standard library (see go.mod) so the
// runner image stays small and fast to build.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// apiURL returns the base URL of the app under test. The suite fails loudly
// when API_URL is missing instead of silently falling back to a default that
// would mask a broken compose wiring.
func apiURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("API_URL")
	if u == "" {
		t.Fatal("API_URL environment variable must be set (see docker-compose.test.yml)")
	}
	return u
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// healthResponse mirrors the fields of internal/server.HealthStatus that the
// suite asserts on.
type healthResponse struct {
	Status string                     `json:"status"`
	Checks map[string]json.RawMessage `json:"checks"`
}

// waitForHealthy polls /health until the app reports overall status
// "healthy" (DB connected, migrations applied) or the deadline passes.
// The endpoint always returns 200 and reports "degraded" while lazy DB
// initialization is still in flight, so polling on the JSON body is the
// correct readiness signal. The app initializes the database lazily on the
// first API request (never from /health itself), so each iteration nudges an
// API endpoint first; without that the status stays "pending" forever.
func waitForHealthy(t *testing.T, base string, deadline time.Duration) healthResponse {
	t.Helper()
	var last healthResponse
	var lastErr error
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		nudgeLazyInit(base)
		last, lastErr = fetchHealth(base)
		if lastErr == nil && last.Status == "healthy" {
			return last
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("app never became healthy within %s: last status %q, last error %v", deadline, last.Status, lastErr)
	return healthResponse{}
}

// nudgeLazyInit fires a throwaway API request to trigger the app's lazy
// database initialization. Errors are ignored: the subsequent /health poll
// is the actual readiness check.
func nudgeLazyInit(base string) {
	resp, err := httpClient.Get(base + "/api/recommendations")
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func fetchHealth(base string) (healthResponse, error) {
	var h healthResponse
	resp, err := httpClient.Get(base + "/health")
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("GET /health: status %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return h, fmt.Errorf("GET /health: decode body: %w", err)
	}
	return h, nil
}

// TestHealthEndpoint verifies the app boots, connects to Postgres, applies
// migrations and reports fully healthy dependency checks.
func TestHealthEndpoint(t *testing.T) {
	base := apiURL(t)

	h := waitForHealthy(t, base, 90*time.Second)

	for _, check := range []string{"config_store", "auth_store", "migrations"} {
		if _, ok := h.Checks[check]; !ok {
			t.Errorf("health response missing %q check; got checks %v", check, keys(h.Checks))
		}
	}
}

// TestVersionEndpoint verifies the public /version endpoint serves JSON build
// metadata without authentication.
func TestVersionEndpoint(t *testing.T) {
	base := apiURL(t)
	waitForHealthy(t, base, 90*time.Second)

	resp, err := httpClient.Get(base + "/version")
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /version: status %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /version: decode body: %v", err)
	}
	if len(body) == 0 {
		t.Error("GET /version: empty JSON body")
	}
}

// TestAPIRequiresAuth verifies the auth middleware is live end-to-end: an
// unauthenticated request to a protected endpoint must be rejected, not
// served.
func TestAPIRequiresAuth(t *testing.T) {
	base := apiURL(t)
	waitForHealthy(t, base, 90*time.Second)

	resp, err := httpClient.Get(base + "/api/recommendations")
	if err != nil {
		t.Fatalf("GET /api/recommendations: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/recommendations without credentials: status %d, want %d",
			resp.StatusCode, http.StatusUnauthorized)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
