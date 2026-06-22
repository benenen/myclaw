# a2a-mcp: expose live boo sessions as MCP resources (progressive)

## Goal
Work around the codex >=0.120 MCP-**tools** injection bug (verified: codex never
exposes MCP tools to the model, only MCP **resources**) by making `mcps/a2a` expose
live boo sessions as MCP **resources** too. codex agents discover the routable boo
sessions + their capabilities via `list_mcp_resources` / `read_mcp_resource`
(verified working end-to-end against real codex), then **dispatch via the `boo`
CLI directly** (instructed in the bot's system prompt). The existing
`a2a_list` / `a2a_dispatch` **tools stay unchanged** (clients whose MCP-tools path
works — claude/opencode — keep using them). Purely additive, backward compatible.

## Verified gate (already done)
A throwaway demo resource (`boo://sessions` returning a marker) proved real codex
0.137 (via the resolver's exact `-c mcp_servers.a2a…` launch) DOES call
`list_mcp_resources` + `read_mcp_resource` and receives the real content (marker
`BOO_RES_MARKER_4242`). Resources are NOT affected by the tools bug. This redesign
builds on that confirmed fact.

## go-sdk constraint (drives the shape)
go-sdk v0.8.0 `resources/list` returns only **statically registered** resources
(`AddResource`) — there is no per-request "list" hook. Dynamic per-item listing is
served via a **resource template** (`AddResourceTemplate`) whose `read` is handled
dynamically. So the progressive shape is:
- **One static roster resource** `boo://sessions` — read returns the live session
  list (cheap: names + titles, no capability-file reads).
- **One resource template** `boo://session/{name}` — read dynamically returns that
  one session's capability detail.

This realizes "list 拿清单 (read `boo://sessions`) · read 时动态取该会话能力
(read `boo://session/<name>`)".

## Resources

### `boo://sessions` (static resource)
- Name `boo-sessions`, MIME `application/json`,
  Description: "Live boo sessions you can route subtasks to. Read a single
  `boo://session/<name>` for that session's capabilities."
- Read handler → `boo ls --json` (via `runBoo`) → JSON:
  ```json
  {"sessions":[{"name":"nvr","title":"✳ Estimate Rust rewrite…","idle_ms":6428615}]}
  ```
  (the cheap roster — NO `boo.capabilities.json` reads here). `boo` unavailable /
  `ls` failure → `{"sessions":[]}` (best-effort, never errors the read).

### `boo://session/{name}` (resource template)
- Template URI `boo://session/{name}`, Name `boo-session`, MIME `application/json`,
  Description: "Capabilities + live status of one boo session."
- Read handler → parse `{name}` from the request URI (`strings.TrimPrefix` of
  `boo://session/`); confirm it is live via `boo ls --json`; then build JSON:
  ```json
  {"name":"nvr","title":"…","idle_ms":123,"cwd":"/path","capability":"go coder [skills: …]"}
  ```
  `cwd` via `booSessionCwd(name)`; `capability` via
  `booCapabilitiesDescription(cwd)` (empty string when absent — falls back to the
  title client-side). If the name is not a live session → `ResourceNotFoundError(uri)`.

## Components (all in `mcps/a2a/`, reusing existing helpers)
- `resolve`, `runBoo`, `booSession`, `booSessionCwd`, `booCapabilitiesDescription`,
  `booConfigDir` already exist — reuse them.
- New in `a2a.go` (pure, testable via the `runBoo` stub + `t.Setenv`/`t.TempDir`):
  - `booRoster(ctx) []booSession` — `runBoo ls --json` → parsed sessions (empty on
    any error).
  - `booSessionDetail(ctx, name) (SessionDetail, bool)` — find `name` in the roster
    (`false` if not live); fill `Title`/`IdleMS` from the roster entry, `Cwd` +
    `Capability` from the boo helpers.
  - `SessionDetail{Name, Title string; IdleMS int64; Cwd, Capability string}` (json tags).
  - Two small JSON content builders (or inline `json.Marshal`).
- `main.go`: after the two `AddTool` calls, add `server.AddResource(boo://sessions, …)`
  and `server.AddResourceTemplate(boo://session/{name}, …)`. Add `encoding/json` import.

## Dispatch (unchanged transport, codex path = boo CLI)
No new dispatch path in a2a-mcp. codex routes by: read `boo://sessions` → pick →
optionally read `boo://session/<name>` → run `boo send <name> --text "<prompt>"
--enter` then `boo peek <name>` itself (it has shell). The existing `a2a_dispatch`
**tool** is retained for clients that expose MCP tools.

## Bot prompt (rollout, separate step)
Update shiben's `system_prompt` (DB → AGENTS.md, via the per-bot-system-prompt
feature) to a "discover via the a2a `boo://…` resources, then dispatch via the boo
CLL" router prompt. (Operational; see plan's rollout task. Not part of the a2a-mcp
code change.)

## Error handling
- Roster read: `boo` missing / `ls` non-zero / bad JSON → `{"sessions":[]}` (logged
  to stderr), never fails the read.
- Session-detail read: name not in live roster → `ResourceNotFoundError`. Missing
  `.state`/capabilities file → `capability:""`, `cwd:""` (still a valid read).
- Existing tool/HTTP paths unchanged.

## Testing
- `booRoster`: stub `runBoo` (`ls --json` → 2 sessions) → 2 parsed; `ls` exit≠0 /
  exec error / bad JSON → empty.
- `booSessionDetail`: stub roster with "build"; `t.Setenv` XDG + a `build.state` →
  temp cwd with `boo.capabilities.json` → `Capability` set, `Cwd` set; unknown name
  → `false`; live name but no `.state` → `Capability:""`.
- Resource handlers (call the handler funcs directly with a fake `*ReadResourceRequest`):
  `boo://sessions` → JSON has the stubbed sessions; `boo://session/build` → JSON has
  name/title/capability; `boo://session/ghost` (not live) → error is
  `ResourceNotFoundError`.
- Build smoke: `go build -o /tmp/a2a-mcp .`; drive `resources/list` (sees
  `boo://sessions`) + `resources/templates/list` (sees the template) +
  `resources/read boo://sessions` + `resources/read boo://session/<live>` via the
  newline-delimited MCP handshake; assert shapes.
- Real-codex acceptance (isolated, throwaway dir; NOT touching live shiben): launch
  codex with the resolver-style `-c mcp_servers.a2a.command=/tmp/a2a-mcp …`; confirm
  the model reads `boo://sessions` and a `boo://session/<name>` and reports a value
  only obtainable by reading them.
- No test runs the real `boo` for unit tests (runBoo stubbed); the smoke/acceptance
  may use real boo sessions.

## Out of scope (v1)
- Removing or changing `a2a_list`/`a2a_dispatch` tools.
- Exposing HTTP a2a servers as resources (only boo sessions become resources now).
- Dynamically registering one concrete resource per session in `resources/list`
  (go-sdk lists only static resources; roster + template is the chosen shape).
- A new dispatch transport (codex uses boo CLI; tool path retained for others).

## Notes
- Branch `feat/a2a-boo-resources` off `main`. Only `mcps/a2a/{a2a.go,a2a_test.go,
  main.go}` change. No new deps (`encoding/json`, `strings`, `context` already used).
- Deploy = merge to main + `make mcp-a2a` to rebuild `/usr/local/bin/a2a-mcp`
  (spawned fresh by codex each launch; the air-watched myclaw service does not embed
  it). Implement in an isolated worktree; never edit the live a2a-mcp binary in place.
