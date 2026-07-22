// Package mcp wires the CUDly MCP server: it registers every tool from
// mcp/tools onto a github.com/modelcontextprotocol/go-sdk/mcp.Server and
// builds the cudly_list_commitment_actions catalog from those same tools'
// descriptors, so the live tool set and the discoverability catalog can
// never drift apart.
package mcp

import (
	"fmt"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/mcp/tools"
)

// ServerName is the MCP Implementation.Name this server identifies as.
const ServerName = "cudly-mcp"

// registrations returns every purchase/search tool this server exposes,
// excluding cudly_list_commitment_actions itself (NewServer adds that one
// last, once it has every other tool's Descriptor to build the catalog
// from).
func registrations() []tools.Registration {
	return []tools.Registration{
		tools.NewSearchRecommendationsTool(),
		tools.NewAWSEC2RIPurchaseTool(),
		tools.NewAWSOpenSearchRIPurchaseTool(),
		tools.NewAWSRedshiftRIPurchaseTool(),
		tools.NewAWSMemoryDBRIPurchaseTool(),
		tools.NewAWSRDSRIPurchaseTool(),
		tools.NewAWSElastiCacheRIPurchaseTool(),
	}
}

// NewServer builds the CUDly MCP server with every tool registered. version
// is reported to clients as the server's Implementation.Version (pass the
// build-time version string, or "dev" for unreleased builds). Callers run
// the returned server on a transport, e.g.:
//
//	server, err := mcp.NewServer("1.0.0")
//	server.Run(ctx, &gosdk.StdioTransport{})
func NewServer(version string) (*gosdk.Server, error) {
	s := gosdk.NewServer(&gosdk.Implementation{Name: ServerName, Version: version}, nil)

	regs := registrations()
	descriptors := make([]tools.Descriptor, 0, len(regs)+1)
	for _, r := range regs {
		descriptors = append(descriptors, r.Descriptor())
	}

	listTool := tools.NewListCommitmentActions(descriptors)
	regs = append(regs, listTool)

	for _, r := range regs {
		if err := r.Register(s); err != nil {
			return nil, fmt.Errorf("register tool %q: %w", r.Descriptor().Name, err)
		}
	}

	return s, nil
}
