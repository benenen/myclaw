# mcps/ — Independent stdio MCP-tool Projects

Date: 2026-06-22
Branch: feat/per-bot-mcp-servers
Status: Approved (design)

## Problem

The `mcps/` folder is empty. We want it to hold several **independent Go
projects**, each compiling to its **own MCP-server binary** (a distinct MCP
tool). These binaries are the concrete, launchable counterpart to the
per-bot MCP attachment feature already merged on this branch: a bot is wired
to a server via `domain.MCPServer{ServerType, Command, Args, ...}`, and
`BotCLIResolver.buildMCPConfigJSON` already emits the `--mcp-config` entry
for a stdio (command-based) server. What's missing is the servers themselves.

## Decisions (locked during brainstorming)

- **Transport:** stdio. Each binary speaks MCP over stdin/stdout and is
  launched by a bot through `Command` + `Args`. (Matches the `Command`/`Args`
  fields on `domain.MCPServer` and the stdio branch of `buildMCPConfigJSON`.)
- **Code organization:** each project is its **own nested Go module** with its
  own `go.mod`, tied together by a root **`go.work`**. No `replace` directives,
  no publishing — the workspace resolves the local modules.
- **Starter tools:** two projects — `echo` and `ping` — one tool per module.
  This is the minimal, real demonstration of "multiple projects → multiple MCP
  tool binaries."
- **go.work.sum:** stays gitignored; only `go.work` is committed.
- **SDK:** `github.com/modelcontextprotocol/go-sdk v0.8.0` (same version the
  main module already uses in `internal/app/orchestration/mcp.go`).

## Layout

```
mcps/
  echo/
    go.mod          # module github.com/benenen/myclaw/mcps/echo
    main.go         # NewServer + AddTool(echo) + Run(&mcp.StdioTransport{})
    echo.go         # pure handler: runEcho(EchoInput) EchoOutput
    echo_test.go    # asserts runEcho reflects input (no transport)
  ping/
    go.mod          # module github.com/benenen/myclaw/mcps/ping
    main.go
    ping.go         # pure handler: runPing() PingOutput
    ping_test.go    # asserts runPing returns "pong" + parseable RFC3339 time
go.work             # use ( . ./mcps/echo ./mcps/ping )  — committed
```

Each `mcps/<name>/go.mod` carves its subtree out of the main
`github.com/benenen/myclaw` module, so the projects build and version
independently while the workspace keeps a single resolved build list.

## Component detail

### Pattern (shared by both servers)

Mirrors the existing `internal/app/orchestration/mcp.go` style, but the
transport is stdio and the tool logic is a pure function so it is testable
without a transport:

```go
// main.go
func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Reflect the given text back to the caller.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
		return nil, runEcho(in), nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("echo mcp server: %v", err)
	}
}
```

### echo server

- `EchoInput{ Text string `json:"text" jsonschema:"the text to echo back"` }`
- `EchoOutput{ Text string `json:"text" jsonschema:"the echoed text"` }`
- `runEcho(in EchoInput) EchoOutput` → `EchoOutput{Text: in.Text}` (pure).
- Tool: `echo`.

### ping server

- `PingInput{}` (no fields).
- `PingOutput{ Time string `json:"time"`; Message string `json:"message"` }`.
- `runPing() PingOutput` → `{Time: time.Now().UTC().Format(time.RFC3339), Message: "pong"}`.
- Tool: `ping`.

## go.work

```
go 1.23.6

use (
	.
	./mcps/echo
	./mcps/ping
)
```

`.gitignore` change: remove the two lines under `# Go workspace file`
(`go.work`, `go.work.sum`) and re-add a single `go.work.sum` ignore, so
`go.work` is tracked but `go.work.sum` is not. Also add `/bin/` so built
binaries are not committed.

## Build & test

Makefile additions:

```
mcp-echo:
	go build -o bin/mcp-echo ./mcps/echo

mcp-ping:
	go build -o bin/mcp-ping ./mcps/ping

mcps: mcp-echo mcp-ping

test-mcps:
	go test ./mcps/echo/... ./mcps/ping/...
```

Root `go test ./...` does **not** reach nested modules in workspace mode, so
`test-mcps` lists them explicitly.

## Error handling

- Servers are thin: a transport error from `server.Run` is fatal-logged and
  exits non-zero. Tool handlers for echo/ping cannot fail, so they return a
  `nil` error.
- stdio servers must never write non-protocol noise to **stdout** (it would
  corrupt the JSON-RPC stream). All diagnostics go to **stderr** via the
  default `log` package (which writes to stderr).

## Testing

- `echo_test.go`: `runEcho(EchoInput{Text: "hi"})` returns `EchoOutput{Text: "hi"}`.
- `ping_test.go`: `runPing()` returns `Message == "pong"` and a `Time` that
  parses with `time.Parse(time.RFC3339, ...)`.
- `make mcps` compiles both binaries.
- Manual smoke (optional): pipe an `initialize` + `tools/list` JSON-RPC
  handshake into `bin/mcp-echo` and confirm the `echo` tool is listed.

## Out of scope

- Registering these binaries as `domain.MCPServer` rows or attaching them to a
  bot — done later by the user via the existing mcpserver service / API.
- Any additional tools (fetch, clock, etc.) — future projects added the same way.
- A shared helper module — each project is self-contained for now; extract one
  only if duplication becomes painful.
