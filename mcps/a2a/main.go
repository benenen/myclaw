package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

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
	server.AddResource(&mcp.Resource{
		URI:         "boo://sessions",
		Name:        "boo-sessions",
		Title:       "Live boo sessions",
		Description: "Live boo sessions you can route subtasks to. Read boo://session/<name> for one session's capabilities.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		data, _ := json.Marshal(map[string]any{"sessions": booRoster(ctx)})
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
			{URI: "boo://sessions", MIMEType: "application/json", Text: string(data)},
		}}, nil
	})

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "boo://session/{name}",
		Name:        "boo-session",
		Title:       "boo session detail",
		Description: "Capabilities + live status of one boo session.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		name := strings.TrimPrefix(req.Params.URI, "boo://session/")
		detail, ok := booSessionDetail(ctx, name)
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		data, _ := json.Marshal(detail)
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
			{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)},
		}}, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("a2a mcp server: %v", err)
	}
}
