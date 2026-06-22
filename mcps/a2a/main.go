package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	configPath := flag.String("config", os.Getenv("A2A_SERVERS_CONFIG"), "path to the A2A servers JSON config")
	flag.Parse()

	sources, err := loadSources(*configPath)
	if err != nil {
		log.Printf("a2a: config load failed, continuing with no sources: %v", err)
		sources = nil
	}
	client := newA2AClient(http.DefaultClient)

	server := mcp.NewServer(&mcp.Implementation{Name: "a2a", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "a2a_list", Description: "List the A2A servers (incl. live boo sessions) this agent can dispatch subtasks to."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, ListOutput, error) {
			return nil, runList(resolve(ctx, sources)), nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "a2a_dispatch", Description: "Send a self-contained subtask to a named A2A server and return its result."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in DispatchInput) (*mcp.CallToolResult, DispatchOutput, error) {
			out, err := runDispatch(ctx, sources, client, in)
			return nil, out, err
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("a2a mcp server: %v", err)
	}
}
