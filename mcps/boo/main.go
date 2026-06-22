package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "boo", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "boo_ls", Description: "List boo sessions (name, attached, idle_ms, title)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in LsInput) (*mcp.CallToolResult, LsOutput, error) {
			out, err := runLs(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "boo_new", Description: "Start a detached boo session running an optional command; returns the session name."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in NewInput) (*mcp.CallToolResult, NewOutput, error) {
			out, err := runNew(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "boo_send", Description: "Type text (optionally + Enter) or named keys into a session."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SendInput) (*mcp.CallToolResult, SendOutput, error) {
			out, err := runSend(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "boo_peek", Description: "Read a session's rendered screen (structured JSON; optional scrollback)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in PeekInput) (*mcp.CallToolResult, PeekOutput, error) {
			out, err := runPeek(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "boo_wait", Description: "Block until a session's screen contains text (mode=text) or goes idle (mode=idle); matched=false on timeout."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WaitInput) (*mcp.CallToolResult, WaitOutput, error) {
			out, err := runWait(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "boo_kill", Description: "End a session by name, or all sessions."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in KillInput) (*mcp.CallToolResult, KillOutput, error) {
			out, err := runKill(ctx, in)
			return nil, out, err
		})

	// stdio: diagnostics MUST go to stderr; stdout is the JSON-RPC stream.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("boo mcp server: %v", err)
	}
}
