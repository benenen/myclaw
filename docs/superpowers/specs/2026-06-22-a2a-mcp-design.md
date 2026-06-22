# a2a-mcp Design — an MCP server for A2A sub-agent routing

## Goal
A standalone MCP server project `mcps/a2a` (mirrors `mcps/echo|ping|boo`) that lets
the calling agent **list a configured set of A2A servers** and **dispatch a
self-contained subtask to a chosen one** (agent-to-agent delegation), returning
the result synchronously. The registry of A2A servers is read from a config file
(no dependency on a running myclaw or the main module).

## Scope

In scope — two tools:

| Tool | Input | Output |
|---|---|---|
| `a2a_list` | — | `{servers: [{name, description, endpoint}]}` (auth tokens NOT exposed) |
| `a2a_dispatch` | `{agent_name, prompt}` | `{result: string}` |

`a2a_dispatch` resolves `agent_name` against the registry (error if unknown), POSTs
an A2A `message/send` JSON-RPC call to that server's `endpoint` (with a `Bearer`
header when the entry has an `auth_token`), extracts the text result, and returns
it. **Synchronous** — it returns the immediate result of `message/send`.

Out of scope (v1): asynchronous task polling (`tasks/get`), agent-card
auto-discovery (descriptions come from the config), streaming, and dynamically
registering servers via a tool (the registry is a config file).

## Background: the A2A protocol myclaw speaks
Reuse the protocol shape from `internal/app/orchestration/a2a_client.go` (but
reimplement it standalone — no import of the main module):
- POST JSON-RPC 2.0 to the server's `endpoint`:
  ```json
  {"jsonrpc":"2.0","id":"<rpc-id>","method":"message/send",
   "params":{"message":{"kind":"message","role":"user","messageId":"<msg-id>",
                        "parts":[{"kind":"text","text":"<prompt>"}]}}}
  ```
- Header `Authorization: Bearer <auth_token>` when set; `Content-Type: application/json`.
- Response is JSON-RPC `{result|error}`. The `result` is either a **Message**
  (`parts:[{kind:"text",text}]`) or a **Task** (`status.message.parts` or
  `artifacts[].parts`). Extract and concatenate the text parts (cover both shapes).
- Non-2xx HTTP or a JSON-RPC `error` → tool error.

## Architecture

Standalone Go module `mcps/a2a` (module `github.com/benenen/myclaw/mcps/a2a`,
`go 1.23.6`, `require github.com/modelcontextprotocol/go-sdk v0.8.0` — same as
echo/ping/boo). Add `./mcps/a2a` to `go.work` and `mcp-a2a`/`test-mcps` targets to
the `Makefile`.

Files:
- `main.go` — `mcp.NewServer(&mcp.Implementation{Name:"a2a",Version:"0.1.0"},nil)`,
  loads the registry once at startup, registers the two tools, runs
  `server.Run(ctx, &mcp.StdioTransport{})`. Diagnostics to stderr.
- `a2a.go` — config/registry, the A2A client, and the two tool functions.
- `a2a_test.go` — tests.

### Config / registry
- A `Server` entry: `{Name, Description, Endpoint, AuthToken string}` (JSON tags
  `name, description, endpoint, auth_token`).
- `loadRegistry(path string) ([]Server, error)` reads a JSON array from the file.
- Path resolution at startup: `--config <path>` flag (primary, so it can be passed
  via `myclaw mcp add ... --args --config,/path`); fall back to env
  `A2A_SERVERS_CONFIG`; if neither set or the file is missing → empty registry
  (logged to stderr, NOT fatal — `a2a_list` returns `{servers:[]}`).
- `Registry` type wrapping `[]Server` with `find(name) (Server, bool)`.

### A2A client (standalone, injectable)
```go
type a2aClient struct{ http *http.Client }
func (c *a2aClient) send(ctx context.Context, s Server, prompt string) (string, error)
```
`send` builds the `message/send` body, sets headers, POSTs to `s.Endpoint`, decodes
the JSON-RPC response, and runs `extractText` (Message-or-Task) — a near-copy of
the orchestration client. Message/rpc ids generated locally (e.g.
`crypto/rand` hex, no dependency on `domain.NewPrefixedID`).

### Tool functions (pure-ish, testable)
- `runList(reg Registry) ListOutput` — maps the registry to `{servers:[{name,
  description, endpoint}]}` (omits `auth_token`).
- `runDispatch(ctx, reg Registry, c *a2aClient, in DispatchInput) (DispatchOutput, error)`
  — `reg.find(in.AgentName)` (→ `no such a2a server: <name>` error if missing),
  validates `prompt` non-empty, calls `c.send`, returns `{result}`.

I/O types (jsonschema-tagged): `ListInput struct{}`,
`Server{Name,Description,Endpoint}` (output view, no token), `ListOutput{Servers
[]ServerView}`; `DispatchInput{AgentName,Prompt}`, `DispatchOutput{Result string}`.

### main.go wiring
Build the registry + `&a2aClient{http: http.DefaultClient}` once, then:
```go
mcp.AddTool(server, &mcp.Tool{Name:"a2a_list", Description:"List the A2A servers this agent can dispatch subtasks to."},
  func(ctx, _, in ListInput) (*mcp.CallToolResult, ListOutput, error) { return nil, runList(reg), nil })
mcp.AddTool(server, &mcp.Tool{Name:"a2a_dispatch", Description:"Send a self-contained subtask to a named A2A server and return its result."},
  func(ctx, _, in DispatchInput) (*mcp.CallToolResult, DispatchOutput, error) { out, err := runDispatch(ctx, reg, client, in); return nil, out, err })
```

## Error handling
- Unknown `agent_name` → `no such a2a server: <name>` (before any HTTP).
- Empty `prompt` → validation error before HTTP.
- A2A endpoint non-2xx → `a2a endpoint returned <code>`; JSON-RPC `error` →
  `a2a error <code>: <message>`; transport failure → wrapped error.
- Unrecognized result shape → `unrecognized a2a result`.
- Registry load failure at startup → logged to stderr, empty registry (non-fatal).

## Testing (stub the HTTP transport with `httptest.Server`; no real A2A servers)
- `loadRegistry`: parses a JSON fixture into `[]Server`; missing file → error
  surfaced (and main treats it as empty).
- `runList`: maps registry → output, and **does not leak `auth_token`**.
- `runDispatch` happy path: a `httptest.Server` asserts the request is JSON-RPC
  `message/send` with the prompt in `message.parts[0].text` and the `Bearer` header,
  returns a **Message** result → `runDispatch` returns the text.
- `runDispatch` Task-shape result: server returns a Task (`status.message.parts`)
  → text extracted.
- `runDispatch` unknown agent → `no such a2a server` error (no HTTP made — assert
  the test server was not hit, or just check the error).
- `runDispatch` empty prompt → validation error.
- A2A `error` JSON-RPC and non-2xx → surfaced as errors.
- No test contacts a real network endpoint.

## Assumptions / notes
- The config file is operator-maintained (the "registration"); each entry's
  `endpoint` is a reachable A2A server speaking the `message/send` protocol.
- Wiring to a bot via the per-bot MCP feature:
  ```bash
  cd mcps/a2a && go build -o /usr/local/bin/a2a-mcp .
  myclaw mcp add --name a2a --type stdio --command /usr/local/bin/a2a-mcp --args --config,/etc/a2a/servers.json
  myclaw mcp attach --bot <id> --server a2a
  ```
- Lives alongside `mcps/echo|ping|boo`; only new `mcps/a2a/**` files are added,
  plus one `go.work` line and the `Makefile` targets.
