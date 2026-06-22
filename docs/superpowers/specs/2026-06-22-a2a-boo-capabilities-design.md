# boo session capability panel — boo.capabilities.json in a2a_list

## Goal
When `mcps/a2a` resolves a live `boo` session into an A2A server, enrich its
**`description`** from a `boo.capabilities.json` file in that session's working
directory, so the dispatching agent can route by capability via `a2a_list`. If the
file is absent or unreadable, fall back to the current default (the session title).

## Background — recovering a session's folder
`boo ls --json` / `peek --json` do NOT expose a session's cwd. But the boo daemon
snapshots each session's cwd (for `boo restore`) at
`<boo-config-dir>/<session>.state` — a **single line of plain text = the cwd**
(e.g. `~/.config/boo/myclaw.state` contains `/root/workspace/master/myclaw\n`).
`<boo-config-dir>` resolves as: `dir($BOO_CONFIG)` if `BOO_CONFIG` is set, else
`$XDG_CONFIG_HOME/boo`, else `~/.config/boo`.

## The capabilities file
`<cwd>/boo.capabilities.json` (all fields optional):
```json
{ "description": "A coding agent: edits Go, runs tests", "skills": ["go", "testing"] }
```
The resulting A2A `description` = `description` text, with skills appended as
` [skills: go, testing]` when present. (Per the chosen design, capabilities fold
into the existing `description` string — NO new structured field on the output.)

## Components (added to `mcps/a2a/a2a.go`)
Three small, pure-ish helpers (read real files; tested via `t.Setenv` + temp dirs,
no FS stubbing):
- `booConfigDir() string` — env-driven dir resolution above.
- `booSessionCwd(session string) (string, bool)` — read
  `<booConfigDir>/<session>.state`; trim; `("", false)` on any read error / empty.
- `booCapabilitiesDescription(cwd string) (string, bool)` — read
  `<cwd>/boo.capabilities.json`; JSON-decode `{description, skills}`; build the
  description string (description + ` [skills: …]`); `("", false)` on missing file,
  parse error, or empty result.

## Wiring — `resolve()` boo expansion
For each session from `boo ls --json`, compute the description:
```go
desc := sess.Title
if cwd, ok := booSessionCwd(sess.Name); ok {
    if cap, ok := booCapabilitiesDescription(cwd); ok {
        desc = cap
    }
}
// ResolvedServer{..., Description: desc, ...}
```
Everything else in `resolve` / `dispatchBoo` / the HTTP path is unchanged. The
`a2a_list` ServerView already carries `Description`, so the capability text flows
to the agent with no schema change.

## Error handling
All best-effort: a missing/empty `.state`, a missing/invalid `boo.capabilities.json`,
or an empty description → silently fall back to the session title. Never fails
`a2a_list`. (No stderr spam for the common "no file" case; only log genuinely
unexpected errors if any.)

## Testing (real files via `t.Setenv` + `t.TempDir`; runBoo stubbed for `boo ls`)
- `booSessionCwd`: write `<tmp>/boo/<session>.state` with a path, set
  `XDG_CONFIG_HOME=<tmp>` → returns the trimmed cwd; missing file → `false`.
- `booConfigDir`: `BOO_CONFIG=/x/config.toml` → `/x`; `XDG_CONFIG_HOME=/y` → `/y/boo`;
  neither → `~/.config/boo`.
- `booCapabilitiesDescription`: temp cwd with `boo.capabilities.json`
  {description+skills} → `"… [skills: a, b]"`; description only → plain; missing
  file → `false`; invalid JSON → `false`; empty object → `false`.
- `resolve` enrichment: stub `runBoo` (`boo ls` → one session "build"); set
  `XDG_CONFIG_HOME` to a temp dir with `boo/build.state` → a temp cwd containing
  `boo.capabilities.json` → the resolved boo server's `Description` is the
  capability text. With NO `.state` (or no capabilities file) → `Description` ==
  the session title (fallback).

## Out of scope (v1)
File-change watching (resolve re-reads live on each call anyway); a full A2A
AgentCard; a structured `skills` field on the output; using capabilities to
auto-rank/route inside a2a-mcp (routing stays the dispatching agent's job, informed
by the description).

## Notes
- Branch `feat/a2a-boo-capabilities` off `main`; only `mcps/a2a/{a2a.go,a2a_test.go}`
  change. No new deps (uses `os`, `path/filepath`, `encoding/json`, `strings`).
