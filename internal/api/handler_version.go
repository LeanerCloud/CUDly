package api //nolint:revive // package name matches directory per Go convention

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/events"
)

// VersionResponse holds build-identity metadata for the public /version
// endpoint. It carries no sensitive data (no account IDs, ARNs, or secrets) so
// it is safe to expose unauthenticated. The fields let an operator curl a
// running environment and compare git_sha against the branch HEAD to diagnose
// deploy-lag (an environment still serving a stale build).
type VersionResponse struct {
	Version   string `json:"version"`
	GitSHA    string `json:"git_sha"`
	BuildTime string `json:"build_time"`
}

// getVersion returns the deployed build's version string, git commit SHA, and
// build timestamp. The values are stamped into the binary at build time via
// ldflags and exported to the environment by cmd/server/main.go (reading them
// from env avoids an import cycle between this package and main). When the
// binary was built without ldflags (e.g. a bare `go run`), the fields fall
// back to the same "dev"/"unknown" sentinels main.go declares.
func (h *Handler) getVersion(_ context.Context, _ *events.LambdaFunctionURLRequest) *VersionResponse {
	return &VersionResponse{
		Version:   envOrDefault("VERSION", "dev"),
		GitSHA:    envOrDefault("GIT_SHA", "unknown"),
		BuildTime: envOrDefault("BUILD_TIME", "unknown"),
	}
}

// envOrDefault returns the value of the named env var, or def when it is unset
// or empty.
func envOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
