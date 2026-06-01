package api

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetVersion_Defaults verifies the handler returns the three build-metadata
// fields and falls back to the "dev"/"unknown" sentinels when the build-time
// env vars are unset (the case for a bare `go run` / `go test` binary that was
// never stamped with ldflags). t.Setenv on a separate test ensures we are not
// leaking real CI-injected values into this assertion.
func TestGetVersion_Defaults(t *testing.T) {
	t.Setenv("VERSION", "")
	t.Setenv("GIT_SHA", "")
	t.Setenv("BUILD_TIME", "")

	h := &Handler{}
	resp, err := h.getVersion(context.Background(), &events.LambdaFunctionURLRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "dev", resp.Version)
	assert.Equal(t, "unknown", resp.GitSHA)
	assert.Equal(t, "unknown", resp.BuildTime)
}

// TestGetVersion_FromEnv verifies the handler surfaces the values main.go
// exports to the environment after reading them from ldflags.
func TestGetVersion_FromEnv(t *testing.T) {
	t.Setenv("VERSION", "abc1234")
	t.Setenv("GIT_SHA", "abc1234")
	t.Setenv("BUILD_TIME", "2026-06-01T12:00:00Z")

	h := &Handler{}
	resp, err := h.getVersion(context.Background(), &events.LambdaFunctionURLRequest{})
	require.NoError(t, err)

	assert.Equal(t, "abc1234", resp.Version)
	assert.Equal(t, "abc1234", resp.GitSHA)
	assert.Equal(t, "2026-06-01T12:00:00Z", resp.BuildTime)
}

// TestRouterDispatch_Version verifies GET /version dispatches to the version
// handler as a public (no-auth) route: no session is configured on the Handler,
// so a non-public route would fail auth, but AuthPublic must succeed and return
// a *VersionResponse with the default sentinels.
func TestRouterDispatch_Version(t *testing.T) {
	t.Setenv("VERSION", "")
	t.Setenv("GIT_SHA", "")
	t.Setenv("BUILD_TIME", "")

	ctx := context.Background()
	// No auth/config wired — a public route must not consult either.
	r := NewRouter(&Handler{})

	result, err := r.Route(ctx, "GET", "/version", &events.LambdaFunctionURLRequest{})
	require.NoError(t, err)

	resp, ok := result.(*VersionResponse)
	require.True(t, ok, "expected *VersionResponse, got %T", result)
	assert.Equal(t, "dev", resp.Version)
	assert.Equal(t, "unknown", resp.GitSHA)
	assert.Equal(t, "unknown", resp.BuildTime)
}
