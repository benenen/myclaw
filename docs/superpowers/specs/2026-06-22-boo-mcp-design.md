# boo-mcp Design — an MCP server wrapping the `boo` terminal multiplexer

## Goal
A new standalone MCP server project `mcps/boo` that exposes the **automation
surface** of the `boo` terminal multiplexer (screen/tmux-like, on libghostty) as
typed MCP tools, so an agent can create headless sessions, drive them, read their
screens, and tear them down. It mirrors the existing `mcps/echo` and `mcps/ping`
project structure.

## Scope

In scope — six tools (the script-driving / "boo help automation" subset):

| Tool | `boo` invocation | Notes |
|---|---|---|
| `boo_ls` | `boo ls --json` | list sessions |
| `boo_new` | `boo new [name] -d [--rows N] [--cols N] [--cwd DIR] -- cmd…` | **always `-d`** (no TTY); prints the session name |
| `boo_send` | `boo send <name> [--text … [--enter]] \| [--key …]` | text XOR keys |
| `boo_peek` | `boo peek <name> --json [--scrollback]` | **always `--json`** (structured screen) |
| `boo_wait` | `boo wait <name> (--text … \| --idle) [--timeout …]` | timeout → not an error |
| `boo_kill` | `boo kill <name> \| --all` | one or all |

Out of scope (v1): interactive `attach`/`ui` (require a real TTY); `rename`,
`restore`, `version` (low value for the automation subset).

## Background: the `boo` CLI
- Binary on `PATH` (here `/usr/local/bin/boo`). Everything except `attach` works
  without a terminal.
- **Exit codes:** `0` success · `1` error · `2` usage error · `3` no such session
  · `4` wait timed out.
- **Machine-readable output:**
  - `boo ls --json` → `[{"name","attached","idle_ms","title"}]`
  - `boo peek --json` → `{"session","title","rows","cols","cursor":{"row","col"},"screen"}`
- `boo new -d` prints the resulting session name on stdout.
- `boo send` is literal: `--text` (no implicit newline), `--enter` appends Enter,
  `--key <comma,list>` for named keys (`Enter`, `C-c`, `Up`, …); `--text` and
  `--key` are mutually exclusive.
- `boo wait` blocks until `--text <substr>` appears or `--idle` (2s quiet);
  `--timeout <dur>` (default 30s), durations like `500ms`, `2s`, `1m`.

## Architecture

Standalone Go module `mcps/boo` (mirrors `mcps/echo`):
- `go.mod` — `module github.com/benenen/myclaw/mcps/boo`, `go 1.23.6`,
  `require github.com/modelcontextprotocol/go-sdk v0.8.0`.
- `main.go` — `mcp.NewServer(&mcp.Implementation{Name:"boo", Version:"0.1.0"}, nil)`,
  registers the six tools via `mcp.AddTool`, runs `server.Run(ctx, &mcp.StdioTransport{})`.
  Diagnostics to stderr; stdout is the JSON-RPC stream.
- `boo.go` — the tool logic.
- `boo_test.go` — tests.
- The module is added to the repo `go.work` (`use ./mcps/boo`) alongside echo/ping,
  and the `Makefile` mcps targets if they enumerate modules.

### The injectable runner (testability)
All tools shell out to the `boo` binary through a single package-level seam so
tests need neither the binary nor live sessions:
```go
// runBoo executes `boo <args>` and returns stdout, the process exit code, and a
// non-nil err only for failures that are NOT a normal boo non-zero exit
// (e.g. binary not found). Overridable in tests.
var runBoo = func(ctx context.Context, args ...string) (stdout []byte, exitCode int, err error) {
    cmd := exec.CommandContext(ctx, "boo", args...)
    var out, errb bytes.Buffer
    cmd.Stdout, cmd.Stderr = &out, &errb
    err = cmd.Run()
    if exitErr, ok := err.(*exec.ExitError); ok {
        return out.Bytes(), exitErr.ExitCode(), nil // boo ran; non-zero is data, with stderr in errb
    }
    // also capture stderr into the returned error for non-ExitError failures
    ...
    return out.Bytes(), 0, err
}
```
(The exact stderr-plumbing is an implementation detail; the contract is: `err` is
reserved for "couldn't run boo", and a non-zero `exitCode` with stdout/stderr is
returned for boo's own exits so handlers can map exit 3/4.)

### Per-tool shape
Each tool is split into a **pure `argsFor<Tool>(in)`** that builds the `boo` argv
(fully unit-testable, no I/O) and a handler that calls `runBoo`, maps the exit
code, and parses JSON where applicable.

Input/Output structs (jsonschema-tagged, matching echo/ping style). Highlights:
- `boo_ls`: input `struct{}`; output `{ sessions []Session }` where
  `Session{Name string; Attached bool; IdleMs int64; Title string}` parsed from
  `ls --json`.
- `boo_new`: input `{ Name string `json:",omitempty"`; Command []string; Rows,Cols int `omitempty`; Cwd string `omitempty` }`;
  argv always includes `-d`; `Command` (if set) appended after `--`; output `{ Name string }`
  (trimmed stdout — the printed session name).
- `boo_send`: input `{ Name string; Text string `omitempty`; Enter bool `omitempty`; Keys string `omitempty` }`;
  validate exactly one of Text / Keys is set (`text XOR keys`); output `{ Ok bool }`.
- `boo_peek`: input `{ Name string; Scrollback bool `omitempty` }`; argv always
  `--json`; output the parsed peek object `{ Session, Title string; Rows, Cols int; Cursor{Row,Col int}; Screen string }`.
- `boo_wait`: input `{ Name string; Mode string (text|idle); Text string `omitempty`; Timeout string `omitempty` }`;
  validate Mode ∈ {text, idle} and Text present when Mode=text; output `{ Matched bool }`
  (exit 4 → `Matched:false`, not an error).
- `boo_kill`: input `{ Name string `omitempty`; All bool `omitempty` }`; validate
  exactly one of Name / All; output `{ Ok bool }`.

### Error / exit-code mapping (in handlers)
- `0` → success (parse output as above).
- `3` (no such session) → tool error `no such session: <name>`.
- `4` (wait timed out) → `boo_wait` returns `{Matched:false}` (success result); not
  applicable to other tools.
- `1`/`2`/other → tool error carrying boo's stderr (trimmed).
- `runBoo` `err != nil` (couldn't execute `boo`) → tool error
  `boo not available: <err>`.

Input-validation failures (e.g. both Text and Keys on `send`) return an error
**before** calling `runBoo`.

## Testing
Stub `runBoo` in tests; no real boo binary or sessions required.
- **argv builders (pure):** `argsForLs/New/Send/Peek/Wait/Kill` produce the exact
  expected `[]string` for representative inputs (e.g. `boo_new{Name:"build",
  Command:["bash"]}` → `["new","build","-d","--","bash"]`; `boo_send` with `Text`
  + `Enter` → `["send","build","--text","make","--enter"]`; `boo_peek{Scrollback}`
  → `["peek","build","--json","--scrollback"]`).
- **output parsing:** `ls --json` and `peek --json` fixtures parse into the structs.
- **exit-code mapping:** stub returns exitCode 3 → no-such-session error; 4 on a
  `boo_wait` → `{Matched:false}`; 1 with stderr → error carrying stderr.
- **validation:** `send` with both/neither Text & Keys → error; `wait` Mode=text
  with empty Text → error; `kill` with neither/both Name & All → error.
- No test invokes the real `boo` binary (keeps the suite hermetic; a manual smoke
  against real boo is a separate verification step).

## Assumptions / notes
- `boo` must be installed on `PATH` wherever this stdio server runs.
- Lives under the existing parallel `mcps/` effort on branch
  `feat/per-bot-mcp-servers`; only new `mcps/boo/**` files are added (plus a
  one-line `go.work` `use` entry), leaving `mcps/echo` and `mcps/ping` untouched.
- Once built, an operator wires it to a bot via the per-bot MCP feature, e.g.
  `myclaw mcp add --name boo --type stdio --command <path-to-boo-mcp-binary>` then
  `myclaw mcp attach --bot <id> --server boo`.
