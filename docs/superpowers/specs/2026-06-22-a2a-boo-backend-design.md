# a2a-mcp boo backend — auto-register boo sessions as A2A servers

## Goal
Extend `mcps/a2a` so a single `{"kind":"boo"}` config source **auto-registers every
live `boo` session as an A2A server**: `a2a_list` shows them, and `a2a_dispatch`
to one drives that boo session (type the prompt in, wait for it to settle, return
the new terminal output). The existing **HTTP path is unchanged**.

## Model: config is a list of "sources" by `kind`
Each config entry is a `Source` with a `kind`:

| kind | meaning | fields |
|---|---|---|
| `http` (or empty — default) | one explicit A2A server (current behavior) | `name`, `description`, `endpoint`, `auth_token?` |
| `boo` | a provider that enumerates live boo sessions | `wait_timeout?` (default `60s`) |

```json
[
  {"kind":"http","name":"weatherbot","endpoint":"https://x/a2a","auth_token":"…"},
  {"kind":"boo","wait_timeout":"60s"}
]
```
Backward compatible: an entry with no `kind` is treated as `http`, so existing
configs keep working unchanged. `kind` is extensible — future kinds slot into the
same source→resolve machinery.

## Dynamic resolution (recomputed on every call)
A `resolve(ctx, []Source, runBoo) ([]ResolvedServer, error)` step turns the static
source list into the live server list:
- **http source** → passes through as a `ResolvedServer{Kind:"http", Name,
  Description, Endpoint, AuthToken}`.
- **boo source** → runs `boo ls --json`; for each session emits a
  `ResolvedServer{Kind:"boo", Name:<session>, Description:<session title>,
  Session:<session>, WaitTimeout:<source.WaitTimeout or "60s">}`.

`a2a_list` and `a2a_dispatch` both call `resolve` first — so **newly-created boo
sessions appear automatically** (that's the "auto-register"), and dispatch always
targets the current set.

- `a2a_list` → `resolve` → `[]ServerView{Name, Description, Endpoint, Kind}` (no
  auth tokens; `Endpoint` empty for boo entries).
- `a2a_dispatch(agent_name, prompt)` → `resolve` → find by `Name` (→ `no such a2a
  server` if missing) → branch on `Kind`:
  - `http` → existing `a2aClient.send` (JSON-RPC `message/send`) — **unchanged**.
  - `boo` → `dispatchBoo`.

If a boo session name collides with an http server name, the http entry wins
(http sources are resolved first; boo sessions are appended and skipped on
duplicate name).

## boo dispatch — line-count scrollback delta
`dispatchBoo(ctx, runBoo, session, prompt, waitTimeout) (string, error)`:
1. `boo peek <session> --scrollback` → record the line count `N` of the
   (append-only) scrollback history. (Session missing → `boo` exit 3 → return
   `boo session not running: <session>` error.)
2. `boo send <session> --text <prompt> --enter`.
3. `boo wait <session> --idle --timeout <waitTimeout>`. **Timeout (exit 4) is
   non-fatal** — proceed to peek anyway (partial output is still useful).
4. `boo peek <session> --scrollback` again → take `lines[N:]` (the newly-added
   history).
5. Light trim: drop a leading line that is the echoed prompt (contains the sent
   prompt text), and drop a trailing line that looks like a shell prompt
   (matches `^\S*[$#%>]\s*$`). Return the remaining joined text.

This is a best-effort heuristic (documented as such): a terminal session is not a
clean request/response endpoint.

## Components / `runBoo` seam
`a2a-mcp` shells out to the `boo` binary through a single injectable seam (copied
from boo-mcp's pattern), so tests need neither `boo` nor live sessions:
```go
var runBoo = func(ctx context.Context, args ...string) (stdout []byte, exitCode int, err error)
```
`resolve` (the `boo ls --json` call) and `dispatchBoo` (peek/send/wait) both go
through it. Tests stub `runBoo` to return canned `ls --json` / scrollback output.

## Type changes (refactor of the existing flat registry)
The current `Server`/`Registry`/`loadRegistry`/`runDispatch(ctx, Registry,
*a2aClient, DispatchInput)` flatten model is replaced:
- `Source{Kind, Name, Description, Endpoint, AuthToken, WaitTimeout string}` — a
  config entry (JSON tags incl. `kind`, `wait_timeout`).
- `loadSources(path) ([]Source, error)` (renamed `loadRegistry`; empty path →
  empty; missing/invalid → error).
- `ResolvedServer{Name, Description, Kind, Endpoint, AuthToken, Session, WaitTimeout string}`.
- `resolve(ctx, []Source, runBoo) ([]ResolvedServer, error)`.
- `ServerView{Name, Description, Endpoint, Kind string}` (+ `Kind`).
- `runList(servers []ResolvedServer) ListOutput`.
- `runDispatch(ctx, []Source, *a2aClient, DispatchInput) (DispatchOutput, error)` —
  resolves, finds, branches; calls `runBoo` for the boo branch (via the seam).
Existing list/dispatch tests are updated to the new signatures; the HTTP
`a2aClient.send`/`extractText` and their httptest tests are unchanged.

## main.go
Load sources via `loadSources(--config / A2A_SERVERS_CONFIG)`; build the
`a2aClient`; register `a2a_list` (`resolve`→`runList`) and `a2a_dispatch`
(`runDispatch`). Registry load error → empty sources (non-fatal, logged to stderr).

## Error handling
- Unknown `agent_name` (after resolve) → `no such a2a server: <name>`.
- Empty `prompt` → validation error before any work.
- boo session not running (boo exit 3) → `boo session not running: <session>`.
- boo wait timeout (exit 4) → non-fatal; return whatever delta was produced.
- `boo` binary missing / `boo ls` fails inside `resolve` → the boo source
  contributes zero servers and `resolve` logs to stderr (does NOT fail the whole
  list — http sources still resolve). `dispatchBoo` runBoo exec failure → `boo not
  available` error.
- HTTP path errors unchanged (non-2xx, json-rpc error, transport).

## Testing (stub `runBoo`; httptest for http; no real boo or A2A servers)
- `loadSources`: parses kinds incl. empty-kind→http default.
- `resolve`: http source passthrough; a boo source with a stubbed `boo ls --json`
  (2 sessions) → 2 `kind:boo` ResolvedServers (name/description/session set,
  wait_timeout defaulted); boo `ls` failure → boo source yields 0, http still
  present.
- `runList`: maps resolved → ServerView incl. `Kind`, still no token.
- `dispatchBoo`: stub `runBoo` so the 1st `peek --scrollback` returns N lines, the
  2nd returns N + new lines → assert the delta is extracted and the prompt-echo
  leading line + shell-prompt trailing line are trimmed; session-missing (exit 3)
  → error; wait timeout (exit 4) → still returns the delta.
- `runDispatch`: http kind still hits the httptest server (unchanged behavior);
  boo kind routes to `dispatchBoo`; unknown name → error; empty prompt → error.
- No test runs the real `boo` binary or contacts a network endpoint.

## Out of scope (v1)
Session filtering / name prefixing on the boo source; per-call timeout override;
marker-based precise extraction; streaming; turning http→boo or auto-discovery of
non-boo sources.

## Notes
- This lands on `feat/a2a-boo-backend` off `main` (a2a-mcp is already on main).
  Only `mcps/a2a/**` changes (no other module, no Makefile/go.work change needed —
  the `mcp-a2a` target + go.work entry already exist).
- `boo` must be installed on `PATH` wherever this a2a-mcp runs for the boo source
  to enumerate/drive sessions; without it the boo source is simply empty.
