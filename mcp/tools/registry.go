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
	// Annotations are the MCP tool hints (ReadOnlyHint, DestructiveHint,
	// IdempotentHint, OpenWorldHint) plus a human-readable Title, shared
	// verbatim with the live mcp.Tool registration -- the same drift
	// protection Description gets. Every Descriptor must set this
	// explicitly via readOnlyAnnotations or purchaseAnnotations: per the MCP
	// spec (see the go-sdk's ToolAnnotations doc comments), a nil
	// DestructiveHint/OpenWorldHint pointer defaults to TRUE on the wire, so
	// a nil Annotations field would silently publish "destructive,
	// open-world, not read-only" for a tool that is neither. There is no
	// safe zero value.
	Annotations *mcp.ToolAnnotations
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

// boolPtr returns a pointer to b. The SDK's DestructiveHint and
// OpenWorldHint are *bool specifically so a server can distinguish "false"
// from "unset" (unset defaults to true on the wire) -- a plain bool zero
// value can't express that, so every explicit hint value must go through a
// pointer.
func boolPtr(b bool) *bool { return &b }

// readOnlyAnnotations builds the ToolAnnotations for a read-only tool:
// ReadOnlyHint true, DestructiveHint explicitly false (a read-only tool
// cannot be destructive by definition), IdempotentHint explicitly false.
// IdempotentHint is documented as "meaningful only when ReadOnlyHint ==
// false", so it does not gate client behavior here, but every Descriptor
// still sets it explicitly rather than relying on the bool zero value, so a
// future spec revision that does read it for read-only tools finds an
// intentional value instead of an accidental one. openWorld distinguishes a
// tool that reaches external systems (search: true) from one that only
// consults an in-process, closed catalog (list_commitment_actions: false).
func readOnlyAnnotations(title string, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: boolPtr(false),
		IdempotentHint:  false,
		OpenWorldHint:   boolPtr(openWorld),
	}
}

// purchaseAnnotations builds the ToolAnnotations shared by every
// money-spending purchase tool: ReadOnlyHint false, DestructiveHint true (D1
// -- Anthropic's review criteria require destructiveHint on any tool that
// modifies state, and "destructive tools always prompt"; the purist
// "purchasing is additive, not destructive" reading was considered and
// rejected), OpenWorldHint true (every purchase reaches a live cloud
// provider API), and IdempotentHint FALSE on every purchase tool, always
// (A5, 00-scope.md §6 decision register) -- even though ExecutePurchase
// derives an idempotency key from the request and dedupes an identical
// retry (mcp/tools/purchase.go), the hint can't express that conditional
// safety: idempotency_nonce is caller-supplied, so one optional string
// turns an otherwise-identical "safe retry" into a second real purchase,
// and provider-side dedupe (AWS ClientToken, Azure/GCP equivalents) is
// time- and scope-bounded, not permanent. The failure asymmetry only points
// one way: a wrongly-true hint risks a client auto-retrying into a
// duplicate multi-thousand-dollar commitment, while a wrongly-false hint
// costs nothing worse than an extra confirmation prompt.
func purchaseAnnotations(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		DestructiveHint: boolPtr(true),
		IdempotentHint:  false,
		OpenWorldHint:   boolPtr(true),
	}
}
