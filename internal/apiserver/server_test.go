package apiserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/config"
	"github.com/LeanerCloud/CUDly/pkg/scorer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testServer(t *testing.T, apiKey string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := config.Config{
		DryRun:   true,
		AuditLog: t.TempDir() + "/audit.jsonl",
		Server: config.ServerConfig{
			Listen:    ":0",
			APIKeyEnv: "TEST_CUDLY_API_KEY",
		},
		Scorer: scorer.Config{},
	}
	if apiKey != "" {
		t.Setenv("TEST_CUDLY_API_KEY", apiKey)
	}
	srv := NewServer(cfg)
	ts := httptest.NewServer(authMiddleware(cfg, newMux(srv)))
	t.Cleanup(ts.Close)
	return srv, ts
}

// newMux returns a fresh mux with all routes registered (used in tests to avoid
// re-creating a full httptest.Server from the real listen address).
func newMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return mux
}

func TestHealth_NoAuth(t *testing.T) {
	_, ts := testServer(t, "secret")
	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuth_MissingToken(t *testing.T) {
	_, ts := testServer(t, "secret")
	resp, err := http.Get(ts.URL + "/recommendations")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuth_InvalidToken(t *testing.T) {
	_, ts := testServer(t, "secret")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/recommendations", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuth_ValidToken(t *testing.T) {
	_, ts := testServer(t, "secret")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/recommendations", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCreateRun_ReturnsID(t *testing.T) {
	_, ts := testServer(t, "")
	body := bytes.NewBufferString(`{"dryRun":true,"autoApprove":false}`)
	resp, err := http.Post(ts.URL+"/runs", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.NotEmpty(t, result["id"])
}

func TestGetRun_NotFound(t *testing.T) {
	_, ts := testServer(t, "")
	resp, err := http.Get(ts.URL + "/runs/nonexistent-id")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestApproveRun_NotPending(t *testing.T) {
	srv, ts := testServer(t, "")

	// Create a run and immediately set it to completed in the store
	run := &Run{
		ID:        "test-run",
		Status:    RunStatusCompleted,
		CreatedAt: time.Now(),
		Request:   RunRequest{DryRun: true},
		approveCh: make(chan struct{}),
		cancelCtx: func() {},
	}
	srv.store.Set("test-run", run)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/runs/test-run/approve", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestCancelRun_AlreadyCompleted(t *testing.T) {
	srv, ts := testServer(t, "")

	run := &Run{
		ID:        "done-run",
		Status:    RunStatusCompleted,
		CreatedAt: time.Now(),
		Request:   RunRequest{DryRun: true},
		cancelCtx: func() {},
	}
	srv.store.Set("done-run", run)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/runs/done-run", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestCancelRun_AlreadyCancelled(t *testing.T) {
	srv, ts := testServer(t, "")

	now := time.Now()
	run := &Run{
		ID:          "cancelled-run",
		Status:      RunStatusCancelled,
		CreatedAt:   now,
		CompletedAt: &now,
		Request:     RunRequest{DryRun: true},
		cancelCtx:   func() {},
	}
	srv.store.Set("cancelled-run", run)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/runs/cancelled-run", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAutoApprove_CompletesWithoutManualApproval(t *testing.T) {
	_, ts := testServer(t, "")

	body := bytes.NewBufferString(`{"dryRun":true,"autoApprove":true}`)
	resp, err := http.Post(ts.URL+"/runs", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var created map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	id := created["id"]

	// Poll until completed (auto-approve run has no external work, should be fast)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		getResp, err := http.Get(ts.URL + "/runs/" + id)
		require.NoError(t, err)
		var run Run
		require.NoError(t, json.NewDecoder(getResp.Body).Decode(&run))
		getResp.Body.Close()
		if run.Status == RunStatusCompleted {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not complete within deadline")
}
