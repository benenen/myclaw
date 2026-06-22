package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Reflect the given text back to the caller.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
		return nil, runEcho(in), nil
	})

	// stdio: diagnostics MUST go to stderr (log defaults to stderr); stdout is the JSON-RPC stream.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("echo mcp server: %v", err)
	}
}
