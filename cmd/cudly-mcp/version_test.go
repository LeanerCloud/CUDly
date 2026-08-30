package main

// Linker injection is exercised only by building and running a subprocess.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	buildTimeout        = 5 * time.Minute
	runTimeout          = 15 * time.Second
	injectedTestVersion = "v0.0.0-version-test"
)

func TestBuiltBinaryReportsInjectedVersion(t *testing.T) {
	binPath := os.Getenv("CUDLY_MCP_TEST_BINARY")
	if binPath == "" {
		binPath = filepath.Join(t.TempDir(), "cudly-mcp")
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "go", "build",
			"-ldflags", "-X main.Version="+injectedTestVersion,
			"-o", binPath, ".")
		cmd.Dir = "."
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "build cmd/cudly-mcp with version ldflags:\n%s", out)
	}

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
		"built binary must report the injected version")
}
