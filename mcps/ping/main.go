package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "ping", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Return the current server time and 'pong'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, PingOutput, error) {
		return nil, runPing(), nil
	})

	// stdio: diagnostics MUST go to stderr (log defaults to stderr); stdout is the JSON-RPC stream.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("ping mcp server: %v", err)
	}
}
