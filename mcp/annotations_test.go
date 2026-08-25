package mcp

import (
	"context"
	"reflect"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/mcp/tools"
)

// allDescriptors returns the Descriptor for every tool this server exposes,
// including cudly_list_commitment_actions -- the same set NewServer builds,
// so tests here cover every registered tool without hand-maintaining a
// second list that could drift from the real registry.
func allDescriptors() []tools.Descriptor {
	regs := registrations()
	descriptors := make([]tools.Descriptor, 0, len(regs)+1)
	for _, r := range regs {
		descriptors = append(descriptors, r.Descriptor())
	}
	descriptors = append(descriptors, tools.ListCommitmentActionsDescriptor())
	return descriptors
}

// connectTestServer builds a real NewServer, connects a real gosdk.Client to
// it over an in-memory transport, and returns the live session -- the same
// end-to-end wiring TestEndToEndSearchThenDryRunPurchase already exercises,
// factored out here so both annotation tests below can drive an actual
// ListTools round trip instead of asserting against Descriptor alone.
func connectTestServer(t *testing.T) *gosdk.ClientSession {
	t.Helper()
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
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestToolAnnotationsMatchDescriptor is the A9 drift test: it proves the
// Annotations the live MCP protocol advertises via ListTools are exactly
// the Annotations each tool's Descriptor() reports, for every registered
// tool. Descriptor.Annotations and each Register()'s mcp.Tool.Annotations
// are two independent call sites (the same duplication Description already
// has) -- nothing in the Go type system stops them from diverging, so this
// is the regression guard.
func TestToolAnnotationsMatchDescriptor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	session := connectTestServer(t)

	toolsList, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	live := make(map[string]*gosdk.ToolAnnotations, len(toolsList.Tools))
	for _, tl := range toolsList.Tools {
		live[tl.Name] = tl.Annotations
	}

	for _, d := range allDescriptors() {
		liveAnnotations, ok := live[d.Name]
		require.Truef(t, ok, "tool %q from the descriptor registry was not returned by ListTools", d.Name)
		assert.Truef(t, reflect.DeepEqual(d.Annotations, liveAnnotations),
			"tool %q: ListTools annotations %+v != Descriptor annotations %+v", d.Name, liveAnnotations, d.Annotations)
	}
}

// TestToolAnnotationsValueAssertions is the A10 value-assertion test: a
// table-driven sweep over every tool's Descriptor (not a hardcoded tool-name
// list, so a future 12th purchase tool cannot dodge it by omission) that
// checks the structural invariants every tool's Annotations must satisfy.
func TestToolAnnotationsValueAssertions(t *testing.T) {
	t.Parallel()

	for _, d := range allDescriptors() {
		d := d
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			require.NotNilf(t, d.Annotations, "tool %q must set Annotations explicitly: a nil Annotations "+
				"publishes destructive=true/openWorld=true/readOnly=false on the wire per the MCP spec", d.Name)
			require.NotNilf(t, d.Annotations.DestructiveHint, "tool %q must set DestructiveHint explicitly "+
				"(nil defaults to true on the wire)", d.Name)
			require.NotNilf(t, d.Annotations.OpenWorldHint, "tool %q must set OpenWorldHint explicitly "+
				"(nil defaults to true on the wire)", d.Name)

			if strings.Contains(d.Action, "purchase") {
				assert.Falsef(t, d.Annotations.ReadOnlyHint,
					"tool %q has a purchase action (%q) so must not be ReadOnlyHint=true", d.Name, d.Action)
				assert.Truef(t, *d.Annotations.DestructiveHint,
					"tool %q has a purchase action (%q) so must be DestructiveHint=true (D1)", d.Name, d.Action)
			}

			assert.NotEmptyf(t, d.Annotations.Title, "tool %q must set a human-readable Title", d.Name)
			assert.NotEqualf(t, d.Name, d.Annotations.Title,
				"tool %q Title must differ from the snake_case tool name", d.Name)
			assert.NotContainsf(t, d.Annotations.Title, "_",
				"tool %q Title %q must be human-readable prose, not a snake_case identifier", d.Name, d.Annotations.Title)
		})
	}
}
