package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Descriptor is the source-of-truth metadata for one MCP tool. Every tool
// file builds one and both mcp/server.go (to know what to register) and
// cudly_list_commitment_actions (to know what to advertise) read it, so the
// live tool set and the discoverability catalog can never drift apart --
// there is exactly one place each tool's name/description/example prompts
// are written.
type Descriptor struct {
	// Name is the MCP tool name, e.g. "cudly_aws_ec2_ri_purchase".
	Name string
	// Provider is "aws", "azure", "gcp", or "" for provider-agnostic
	// meta-tools (cudly_list_commitment_actions, cudly_search_recommendations).
	Provider string
	// Product is the service the tool acts on, e.g. "ec2", "rds", "compute".
	Product string
	// Action is what the tool does, e.g. "ri_purchase", "cud_purchase", "search".
	Action string
	// Description is the tool's full MCP description, shared verbatim with
	// the live mcp.Tool registration so the two can never disagree.
	Description string
	// RealPurchaseEnabled reports whether this tool can execute a real,
	// money-spending purchase today (dry_run=false, confirm=true). false for
	// read-only tools and for tools shipped dry-run-only pending a
	// prerequisite fix (see the Azure/GCP tool comments).
	RealPurchaseEnabled bool
	// ExamplePrompts are 2-3 natural-language prompts that would plausibly
	// invoke this tool, surfaced by cudly_list_commitment_actions so a
	// session that doesn't know the tool name yet can find it.
	ExamplePrompts []string
}

// Registration is implemented by every tool file. Descriptor feeds the
// catalog; Register performs the live mcp.AddTool (or mcp.Server.AddTool)
// call that wires the tool's schema and handler onto the server.
type Registration interface {
	Descriptor() Descriptor
	Register(s *mcp.Server) error
}
