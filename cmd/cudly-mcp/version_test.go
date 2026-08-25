package main

// version_test.go is the regression guard for the version-injection mismatch
// fixed alongside it: the Makefile's shared $(LDFLAGS) has always injected
// -X main.Version=$(VERSION) (see build-server), but this package used to
// declare a lowercase `version` var that ldflags cannot address by name, so
// every release build silently reported "dev" in the MCP initialize
// response's Implementation.Version. An in-process test against
// cudlymcp.NewServer cannot catch this: it calls NewServer directly with a
// Go string, bypassing the ldflags/linker step entirely. Only building the
// real binary with -ldflags and inspecting what it reports over stdio proves
// the wiring works end to end, the same way `make build-mcp` and a real
// release tag do.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	// buildTimeout must cover a genuinely cold build: the "Unit Tests" CI job
	// runs this test with a fresh module/build cache for the non-race build
	// flavor (the preceding `go test -race` step only warms the race-enabled
	// cache), so this recompiles the full AWS/Azure/GCP SDK dependency tree
	// from scratch on shared CI hardware. That measured over 2 minutes in CI
	// (see PR #1891); 5 minutes leaves headroom without masking a real hang.
	buildTimeout = 5 * time.Minute
	runTimeout   = 15 * time.Second

	// injectedTestVersion stands in for a release tag; any value distinct
	// from the "dev" zero value proves ldflags injection reached the binary.
	injectedTestVersion = "v0.0.0-version-test"
)

var (
	buildOnce   sync.Once
	buildDir    string
	builtBinary string
	buildErr    error
)

// TestMain removes the compiled binary's directory once every test in this
// package has run; the binary is built once (buildOnce) and shared across
// tests, so its lifetime is the test binary's, not any single test's.
func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// builtVersionedBinary compiles cmd/cudly-mcp once with -X main.Version set,
// mirroring `make build-mcp`'s ldflags, and returns the path to the
// executable.
func builtVersionedBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "cudly-mcp-version-test-")
		if err != nil {
			buildErr = err
			return
		}
		buildDir = dir
		binPath := filepath.Join(dir, "cudly-mcp")

		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build",
			"-ldflags", "-X main.Version="+injectedTestVersion,
			"-o", binPath, ".")
		cmd.Dir = "." // this package's directory, cmd/cudly-mcp
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build output:\n%s", out)
			return
		}
		builtBinary = binPath
	})
	if buildErr != nil {
		t.Fatalf("build cmd/cudly-mcp with version ldflags: %v", buildErr)
	}
	return builtBinary
}

// TestBuiltBinaryReportsInjectedVersion runs the actual compiled cudly-mcp
// binary as a subprocess over stdio (the same transport every real MCP
// client uses) and asserts the MCP initialize handshake's
// InitializeResult.ServerInfo.Version echoes the ldflags-injected version,
// not the "dev" fallback in cmd/cudly-mcp/main.go.
func TestBuiltBinaryReportsInjectedVersion(t *testing.T) {
	binPath := builtVersionedBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	transport := &gosdk.CommandTransport{Command: exec.CommandContext(ctx, binPath)}
	client := gosdk.NewClient(&gosdk.Implementation{Name: "version-test-client"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	require.NoError(t, err, "connect to the built binary over stdio")
	defer session.Close()

	result := session.InitializeResult()
	require.NotNil(t, result, "InitializeResult")
	require.NotNil(t, result.ServerInfo, "InitializeResult.ServerInfo")
	require.Equal(t, injectedTestVersion, result.ServerInfo.Version,
		"built binary must report the ldflags-injected version, not the main.Version=\"dev\" fallback")
}
