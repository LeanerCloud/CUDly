package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const listCommitmentActionsName = "cudly_list_commitment_actions"

const listCommitmentActionsDescription = "List every CUDly commitment-purchase and search tool available on this " +
	"MCP server, including which ones can execute a REAL purchase (money-affecting) versus which are " +
	"search/preview-only, plus example prompts for each. This tool never spends money and takes no parameters -- " +
	"start here if you don't already know which cudly_* tool you need."

// listCommitmentActionsArgs is empty: the tool takes no parameters.
type listCommitmentActionsArgs struct{}

// ActionEntry is one catalog entry returned by cudly_list_commitment_actions,
// reshaping a Descriptor for JSON output.
type ActionEntry struct {
	Name                string   `json:"name"`
	Provider            string   `json:"provider,omitempty"`
	Product             string   `json:"product,omitempty"`
	Action              string   `json:"action,omitempty"`
	Description         string   `json:"description"`
	RealPurchaseEnabled bool     `json:"real_purchase_enabled"`
	ExamplePrompts      []string `json:"example_prompts,omitempty"`
}

// listCommitmentActionsResult is the tool's structured output.
type listCommitmentActionsResult struct {
	Actions []ActionEntry `json:"actions"`
}

type listCommitmentActionsTool struct {
	descriptors []Descriptor
}

// NewListCommitmentActions builds the cudly_list_commitment_actions tool
// from descriptors -- the same slice of Descriptor values mcp/server.go
// collects from every other tool's Descriptor() method, so this catalog is
// generated from the live registry rather than hand-duplicated in code or
// docs.
func NewListCommitmentActions(descriptors []Descriptor) Registration {
	return &listCommitmentActionsTool{descriptors: descriptors}
}

func (t *listCommitmentActionsTool) Descriptor() Descriptor {
	return Descriptor{
		Name:        listCommitmentActionsName,
		Description: listCommitmentActionsDescription,
		ExamplePrompts: []string{
			"What CUDly tools are available?",
			"Which purchase tools can spend real money right now?",
			"How do I buy AWS EC2 Reserved Instances through CUDly?",
		},
	}
}

func (t *listCommitmentActionsTool) Register(s *mcp.Server) error {
	schema, err := BuildInputSchema[listCommitmentActionsArgs](nil)
	if err != nil {
		return err
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        listCommitmentActionsName,
		Description: listCommitmentActionsDescription,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *listCommitmentActionsTool) handle(_ context.Context, _ *mcp.CallToolRequest, _ listCommitmentActionsArgs) (*mcp.CallToolResult, listCommitmentActionsResult, error) {
	actions := make([]ActionEntry, 0, len(t.descriptors))
	for _, d := range t.descriptors {
		// ActionEntry's fields are identical in name, type, and order to
		// Descriptor's -- only the json tags differ -- so a direct
		// conversion is equivalent to (and clearer than) a field-by-field
		// struct literal.
		actions = append(actions, ActionEntry(d))
	}
	return nil, listCommitmentActionsResult{Actions: actions}, nil
}
