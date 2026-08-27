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
// tool -- in both directions. Descriptor.Annotations and each Register()'s
// mcp.Tool.Annotations are two independent call sites (the same duplication
// Description already has) -- nothing in the Go type system stops them from
// diverging, so this is the regression guard. The tool-count and reverse
// (live-to-descriptor) checks matter because the per-descriptor loop alone
// only proves "every descriptor has a live match" -- a tool registered
// directly against *gosdk.Server outside the Descriptor/Register contract
// (so it never appears in allDescriptors()) could still ship with unsafe or
// missing annotations and this test would stay green without them.
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

	descriptors := allDescriptors()
	require.Lenf(t, toolsList.Tools, len(descriptors),
		"ListTools returned %d tools but the descriptor registry has %d entries -- a tool registered "+
			"outside Descriptor/Register would only surface here, not in the per-name checks below",
		len(toolsList.Tools), len(descriptors))

	descriptorNames := make(map[string]bool, len(descriptors))
	for _, d := range descriptors {
		descriptorNames[d.Name] = true
	}
	for name := range live {
		assert.Truef(t, descriptorNames[name],
			"tool %q is registered on the live server but has no matching Descriptor entry", name)
	}

	for _, d := range descriptors {
		liveAnnotations, ok := live[d.Name]
		require.Truef(t, ok, "tool %q from the descriptor registry was not returned by ListTools", d.Name)
		assert.Truef(t, reflect.DeepEqual(d.Annotations, liveAnnotations),
			"tool %q: ListTools annotations %+v != Descriptor annotations %+v", d.Name, liveAnnotations, d.Annotations)
	}
}

// TestToolAnnotationsValueAssertions is the A10 value-assertion test: a
// table-driven sweep over every tool's Descriptor (not a hardcoded tool-name
// list, so a future 12th purchase tool cannot dodge it by omission) that
// checks every hint's actual value, not just that it is set. A structural
// nil-check alone would pass a purchase tool that mistakenly claims
// IdempotentHint=true or OpenWorldHint=false, or a catalog tool that claims
// OpenWorldHint=true, as long as Descriptor and the live registration agree
// on the same wrong value -- TestToolAnnotationsMatchDescriptor only proves
// the two never drift apart, not that either is correct.
//
// The three-way switch below covers every read-only/purchase role this
// server has today: a tool whose Action contains "purchase" (9 tools), the
// search tool (Action == "search", the only other role that sets Action),
// and cudly_list_commitment_actions (Action left as its zero value ""), the
// one in-process, closed-world catalog tool. Adding a role with a fourth
// annotation profile (e.g. a future audit/server-info tool) will need a new
// case here rather than falling through the default.
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

			switch {
			case strings.Contains(d.Action, "purchase"):
				assert.Falsef(t, d.Annotations.ReadOnlyHint,
					"tool %q has a purchase action (%q) so must be ReadOnlyHint=false", d.Name, d.Action)
				assert.Truef(t, *d.Annotations.DestructiveHint,
					"tool %q has a purchase action (%q) so must be DestructiveHint=true (D1)", d.Name, d.Action)
				assert.Falsef(t, d.Annotations.IdempotentHint,
					"tool %q has a purchase action (%q) so must be IdempotentHint=false (A5): "+
						"idempotency_nonce is caller-supplied, so a claimed-idempotent purchase tool invites a "+
						"client to \"safely\" retry into a duplicate real purchase", d.Name, d.Action)
				assert.Truef(t, *d.Annotations.OpenWorldHint,
					"tool %q has a purchase action (%q) so must be OpenWorldHint=true: every purchase reaches "+
						"a live cloud provider API", d.Name, d.Action)
			case d.Action == "search":
				assert.Truef(t, d.Annotations.ReadOnlyHint, "tool %q is the search tool so must be ReadOnlyHint=true", d.Name)
				assert.Falsef(t, *d.Annotations.DestructiveHint,
					"tool %q is the search tool so must be DestructiveHint=false", d.Name)
				assert.Falsef(t, d.Annotations.IdempotentHint,
					"tool %q is the search tool so must be IdempotentHint=false: each search bills Cost Explorer "+
						"per request, so claiming idempotency invites a free-retry billing loop", d.Name)
				assert.Truef(t, *d.Annotations.OpenWorldHint,
					"tool %q is the search tool so must be OpenWorldHint=true: it reaches a live Cost Explorer/"+
						"Advisor/Recommender API", d.Name)
			default:
				// The one remaining role today is cudly_list_commitment_actions, the
				// in-process catalog: closed-world by construction (it only ever reads
				// the descriptors slice NewServer built at startup, never a live cloud API).
				assert.Truef(t, d.Annotations.ReadOnlyHint, "tool %q must be ReadOnlyHint=true", d.Name)
				assert.Falsef(t, *d.Annotations.DestructiveHint, "tool %q must be DestructiveHint=false", d.Name)
				assert.Falsef(t, d.Annotations.IdempotentHint, "tool %q must be IdempotentHint=false", d.Name)
				assert.Falsef(t, *d.Annotations.OpenWorldHint,
					"tool %q is the in-process catalog tool so must be OpenWorldHint=false", d.Name)
			}

			assert.NotEmptyf(t, d.Annotations.Title, "tool %q must set a human-readable Title", d.Name)
			assert.NotEqualf(t, d.Name, d.Annotations.Title,
				"tool %q Title must differ from the snake_case tool name", d.Name)
			assert.NotContainsf(t, d.Annotations.Title, "_",
				"tool %q Title %q must be human-readable prose, not a snake_case identifier", d.Name, d.Annotations.Title)
		})
	}
}
