package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/mcp/tools"
)

func TestNewServerBuildsWithoutError(t *testing.T) {
	t.Parallel()
	s, err := NewServer("test")
	require.NoError(t, err)
	require.NotNil(t, s)
}

// TestRegistryNonEmpty proves the tool registry is never accidentally empty:
// cudly_list_commitment_actions is always present, even before any
// purchase/search tool has been registered.
func TestRegistryNonEmpty(t *testing.T) {
	t.Parallel()
	regs := registrations()
	descriptors := make([]tools.Descriptor, 0, len(regs)+1)
	for _, r := range regs {
		descriptors = append(descriptors, r.Descriptor())
	}
	listTool := tools.NewListCommitmentActions(descriptors)
	descriptors = append(descriptors, listTool.Descriptor())

	require.NotEmpty(t, descriptors)
	names := make(map[string]bool, len(descriptors))
	for _, d := range descriptors {
		names[d.Name] = true
	}
	assert.True(t, names["cudly_list_commitment_actions"])
}

// TestRealPurchaseToolsDocumentMoneyImpactAndDryRun proves every tool that
// can execute a real purchase leads its description with a money-impact
// statement and a dry-run recommendation (design doc §3/§5) -- so any future
// purchase tool that forgets one fails this test rather than shipping a
// tool description that quietly omits the safety framing every other
// purchase tool carries.
// TestEndToEndSearchThenDryRunPurchase drives the real MCP protocol path
// end to end -- a real Client connected to the real NewServer over an
// in-memory transport, not a bare Go function call -- through the exact
// chain the README's worked example describes: list the catalog, then call
// cudly_aws_ec2_ri_purchase with dry_run=true. It proves the tool schema
// registered without error (AddTool's schema inference/validation runs at
// connect time) and that a dry-run purchase returns structured cost JSON
// without any AWS credentials configured in this test environment --
// confirming dry_run=true never reaches the real purchase path even through
// the full protocol stack, not just the Go-level unit tests in
// mcp/tools/aws_ec2_ri_test.go.
func TestEndToEndSearchThenDryRunPurchase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	server, err := NewServer("test")
	require.NoError(t, err)

	clientTransport, serverTransport := gosdk.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := gosdk.NewClient(&gosdk.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(toolsList.Tools))
	for _, tl := range toolsList.Tools {
		names[tl.Name] = true
	}
	assert.True(t, names["cudly_list_commitment_actions"])
	assert.True(t, names["cudly_aws_ec2_ri_purchase"])

	result, err := session.CallTool(ctx, &gosdk.CallToolParams{
		Name: "cudly_aws_ec2_ri_purchase",
		Arguments: map[string]any{
			"region":         "us-east-1",
			"instance_type":  "m5.large",
			"count":          3,
			"term_years":     3,
			"payment_option": "no-upfront",
			// dry_run/confirm omitted: must default to true/false.
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "dry_run purchase must not be a tool error")

	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var resp tools.PurchaseResponse
	require.NoError(t, json.Unmarshal(structured, &resp))
	assert.True(t, resp.DryRun)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)
}

// TestListCommitmentActionsIncludesItself is the regression guard for the
// CodeRabbit finding that NewServer built the descriptors slice passed to
// cudly_list_commitment_actions before appending the list tool itself to
// regs, so the catalog the tool actually returns at runtime never included
// its own entry despite its description claiming to list "every" tool.
// Unlike TestRegistryNonEmpty (which builds its own descriptors slice by
// hand) this drives the real NewServer wiring and calls the live tool over
// the protocol, so it fails if NewServer regresses even if the manual
// helper above stays correct.
func TestListCommitmentActionsIncludesItself(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	server, err := NewServer("test")
	require.NoError(t, err)

	clientTransport, serverTransport := gosdk.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := gosdk.NewClient(&gosdk.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(ctx, &gosdk.CallToolParams{
		Name:      "cudly_list_commitment_actions",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "cudly_list_commitment_actions must not itself error")

	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var catalog struct {
		Actions []tools.ActionEntry `json:"actions"`
	}
	require.NoError(t, json.Unmarshal(structured, &catalog))

	var names []string
	for _, a := range catalog.Actions {
		names = append(names, a.Name)
	}
	assert.Contains(t, names, "cudly_list_commitment_actions",
		"the catalog cudly_list_commitment_actions returns must include its own entry, got: %v", names)
}

func TestRealPurchaseToolsDocumentMoneyImpactAndDryRun(t *testing.T) {
	t.Parallel()
	for _, r := range registrations() {
		d := r.Descriptor()
		if !d.RealPurchaseEnabled {
			continue
		}
		lower := strings.ToLower(d.Description)
		assert.Truef(t, strings.Contains(lower, "real money") || strings.Contains(lower, "spends money"),
			"tool %q description must state its money impact: %q", d.Name, d.Description)
		assert.Truef(t, strings.Contains(lower, "dry_run") || strings.Contains(lower, "dry run"),
			"tool %q description must recommend dry_run first: %q", d.Name, d.Description)
	}
}
