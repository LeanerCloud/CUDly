// Command cudly-mcp runs the CUDly MCP server on stdio, exposing CUDly's
// RI/SP/CUD search and purchase tools to any MCP client (e.g. Claude Code
// via ~/.claude/mcp.json). See mcp/README.md for setup and usage.
//
// This binary is intentionally separate from the ri-helper CLI (cmd/main.go):
// it is a local/desktop MCP server, never a Lambda handler, and is kept out
// of iac/ and terraform/ (see mcp/README.md "Deployment model").
package main

import (
	"context"
	"log"
	"os"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	cudlymcp "github.com/LeanerCloud/CUDly/mcp"
	_ "github.com/LeanerCloud/CUDly/providers/aws"
	_ "github.com/LeanerCloud/CUDly/providers/azure"
	_ "github.com/LeanerCloud/CUDly/providers/gcp"
)

// version is overridable at build time via:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/cudly-mcp
var version = "dev"

func main() {
	server, err := cudlymcp.NewServer(version)
	if err != nil {
		log.Fatalf("cudly-mcp: failed to build server: %v", err)
	}

	if err := server.Run(context.Background(), &gosdk.StdioTransport{}); err != nil {
		log.Printf("cudly-mcp: server exited with error: %v", err)
		os.Exit(1)
	}
}
