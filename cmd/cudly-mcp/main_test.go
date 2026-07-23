package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cudlymcp "github.com/LeanerCloud/CUDly/mcp"
)

// isolateFromAmbientAWS points the AWS SDK at deliberately nonexistent
// profile/config/credentials so config.LoadDefaultConfig cannot resolve any
// real credentials -- neither from a dev machine's ~/.aws files nor from the
// network (IMDS, ECS/EKS container credential endpoints, web identity). This
// is required for TestRealPurchasePastProviderRegistration below: that test
// drives a real (non-dry-run) purchase call, so it must be impossible for it
// to reach an actual AWS account or make an actual network call, in this or
// any other environment the test happens to run in.
func isolateFromAmbientAWS(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_PROFILE", "cudly-mcp-regression-test-nonexistent-profile")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-credentials"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-config"))
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	t.Setenv("AWS_ROLE_ARN", "")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
}

// TestRealPurchasePastProviderRegistration is the regression guard for the
// bug this file's blank imports fix: cudly-mcp never imported
// providers/aws|azure|gcp, so their init()-registered factories were never
// added to provider.CreateProvider's registry, and every real (non-dry-run)
// purchase failed at ResolveClient with "provider aws is not registered"
// before ever reaching AWS.
//
// This test MUST live in package main under cmd/cudly-mcp/ -- go test
// ./mcp/... does not catch this bug even with the blank imports reverted,
// because a test binary for the mcp or mcp/tools package never pulls in
// cmd/cudly-mcp's imports. Only a test in this package has the blank
// imports in its own dependency graph, so only here does reverting them
// actually flip provider.CreateProvider("aws") back to unregistered.
//
// The test asserts the purchase attempt gets PAST registration and fails for
// a completely different, credentials-shaped reason ("AWS is not
// configured", from providers/aws/provider.go's GetServiceClient) instead of
// "not registered". It never reaches AWS: isolateFromAmbientAWS makes
// config.LoadDefaultConfig fail to resolve the (deliberately nonexistent)
// named profile before any credential lookup or network call happens.
func TestRealPurchasePastProviderRegistration(t *testing.T) {
	isolateFromAmbientAWS(t)

	server, err := cudlymcp.NewServer("test-regression")
	require.NoError(t, err)

	clientTransport, serverTransport := gosdk.NewInMemoryTransports()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := gosdk.NewClient(&gosdk.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(ctx, &gosdk.CallToolParams{
		Name: "cudly_aws_ec2_ri_purchase",
		Arguments: map[string]any{
			"region":         "us-east-1",
			"instance_type":  "m5.large",
			"count":          1,
			"term_years":     1,
			"payment_option": "no-upfront",
			"dry_run":        false,
			"confirm":        true,
		},
	})
	require.NoError(t, err, "CallTool itself must not return a transport-level error")
	require.True(t, result.IsError, "a failed real purchase must surface as a tool error, not a transport error")

	text := result.Content[0].(*gosdk.TextContent).Text
	assert.NotContains(t, strings.ToLower(text), "not registered",
		"provider must be registered for the cudly-mcp binary: got %q", text)
	// providers/aws/provider.go's GetServiceClient (via AWSProvider.IsConfigured)
	// returns exactly this string when config.LoadDefaultConfig cannot resolve
	// the requested profile -- observed and confirmed stable in this test run.
	assert.Contains(t, text, "AWS is not configured",
		"expected a credentials/config-shaped failure once past registration: got %q", text)
}
