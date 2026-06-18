# Per-Bot Workspace + Persisted CLI Session Design

## Goal
Give each bot a persisted **workspace** (the working directory its agent CLI runs in, defaulted from the bot id) and persist each **(bot, CLI) session id** so that when the CLI is (re)started, an existing session is resumed and conversation continuity survives process/server restarts. If no stored session exists for that (bot, CLI), the CLI starts fresh (no resume flag passed).

## Scope

In scope:
- `bot.Workspace` field (persisted, defaulted at creation, overridable), used by the resolver as the CLI working directory.
- New `bot_cli_sessions` table keyed by `(bot_id, cli_type)` storing the CLI's native session id.
- A CLI-agnostic seam: `agent.Spec.ResumeSessionID` (in) and `agent.Response.SessionID` (out).
- Per-driver capture + resume wiring for claude, codex, and opencode (best-effort resume with fallback-to-fresh).
- Save the session id during execution (after a turn); look it up and pass it on CLI start.

Out of scope:
- A UI for editing the workspace or viewing sessions (workspace is defaulted automatically; no new UI this round).
- Cross-bot session sharing.
- Migrating/relocating existing workspace folders when a workspace value changes (the field is set once at creation; changing it later just points future launches elsewhere).
- Guaranteeing codex/opencode resume across restarts where the CLI itself does not support it — those are best-effort (see Risks).

## Decisions (locked during brainstorming)
1. **Data model:** a separate `bot_cli_sessions` table keyed by `(bot_id, cli_type)` — not a single column on the bot — so switching a bot's CLI does not lose the other CLI's session.
2. **CLI scope:** all three CLIs (claude/codex/opencode) wired through the same seam.
3. **`cli_type` key:** the runtime type string ∈ {`claude`,`codex`,`opencode`}. This is both `capability.Key` (available at resolve/lookup time) and `agent.Response.RuntimeType` (available at save time) — verified identical, so no normalization is needed.
4. **Workspace:** a real persisted field, defaulted at bot creation to the computed path, overridable.
5. **Resume is best-effort:** if the CLI rejects the stored session id, the driver starts a fresh session and the new id overwrites the stored one. Empty stored id ⇒ no resume flag passed.

## Background (current state)
- The resolver already sets `spec.WorkDir = filepath.Join(workspaceRoot, botID, "workspace")` (`cli_resolver.go:98`) and `MkdirAll`s it. `config.BotWorkspacePath(botID)` computes the same path. So "workspace = a per-bot folder" exists at runtime; this design persists it as a field.
- `capability.Key` ∈ {codex, claude, opencode} (`discoverer.go:27-29`); `agent.Response.RuntimeType` is set to the same `runtimeType*` constants by every driver. They match exactly.
- Per-CLI session handling today: claude parses `session_id` from its `system/init` event (`SessionID json:"session_id"`); opencode does `session/new` per process in `ensureSession` and caches `session.id`; codex-exec already runs `resume --last`, and codex-acp uses an app-server protocol.

## Architecture

### 1. Data model
- `internal/domain/entities.go`: `Bot` gains `Workspace string`.
- `internal/store/models/bot.go`: GORM `Workspace` column (`workspace`, not null default '').
- Migration **`000006_bot_workspace.up.sql`** (append-only):
  ```sql
  ALTER TABLE bots ADD COLUMN workspace TEXT NOT NULL DEFAULT '';
  ```
- New entity `internal/domain` `BotCLISession{BotID, CLIType, SessionID, WorkDir, UpdatedAt}` + repository interface `BotCLISessionRepository{ Upsert(ctx, BotCLISession) error; Get(ctx, botID, cliType string) (BotCLISession, error) }` (Get returns `ErrNotFound` when absent).
- `internal/store/models/bot_cli_session.go`: GORM model with composite primary key.
- Migration **`000007_bot_cli_sessions.up.sql`** (append-only):
  ```sql
  CREATE TABLE IF NOT EXISTS bot_cli_sessions (
    bot_id     TEXT NOT NULL,
    cli_type   TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    work_dir   TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (bot_id, cli_type)
  );
  ```
- `internal/store/repositories/bot_cli_session_repository.go`: `Upsert` (insert-or-replace on the composite key), `Get`.

### 2. CLI-agnostic seam
- `internal/agent/types.go`:
  - `Spec` gains `ResumeSessionID string` (the session to resume on start; empty ⇒ none).
  - `Response` gains `SessionID string` (the CLI's session id, surfaced after a turn). `cloneSpec` already shallow-copies scalar fields, so `ResumeSessionID` is preserved through the session boundary.

### 3. Per-CLI capture + resume

**claude (`internal/agent/claude/driver_acp.go`):**
- `buildACPArgs` gains a `resumeSessionID string` param (threaded from `spec.ResumeSessionID` in `Init`); when non-empty and the command is the real claude binary, append `"--resume", resumeSessionID` to the injected flags.
- Capture: the driver already parses `system/init.session_id` into its struct; store it on the runtime and set it on the returned `agent.Response.SessionID`.

**opencode (`internal/agent/opencode/driver_acp.go`):**
- `ensureSession`: if `spec.ResumeSessionID != ""` and no cached session, adopt it as `r.sessionID` (skip `session/new`). If a subsequent `session/prompt` fails because the server doesn't know that session id, fall back to `session/new` and continue.
- Capture: surface `r.sessionID` (whether reused or freshly created) on `agent.Response.SessionID`.

**codex (`internal/agent/codex/driver_acp.go`):**
- Capture the conversation/session id the app-server protocol exposes and set it on `agent.Response.SessionID`. (The codex-exec driver already resumes `--last`; for the ACP driver the exact resume call is confirmed against the protocol during implementation.)
- Resume: when `spec.ResumeSessionID != ""`, use codex's resume mechanism; on failure, start fresh.

**Common rule:** resume is best-effort. Any resume failure ⇒ start a new session; the new `Response.SessionID` then overwrites the stored row (see §4).

### 4. Wiring (`internal/app/bot`)

**Pass on start (resolver):**
- `BotCLIResolver` gains a `sessions BotCLISessionRepository` dependency. In `Resolve`, after loading the capability, look up `sessions.Get(botID, capability.Key)`; if found, set `spec.ResumeSessionID = stored.SessionID`. On `ErrNotFound`, leave it empty. (Lookup failure other than `ErrNotFound` is logged and treated as "no resume" — never blocks the turn.)

**Save during execution (orchestrator):**
- `BotMessageOrchestrator` gains the same `sessions` repository. After a successful non-orchestrator turn, if `result.resp.SessionID != ""` and differs from what was passed in, `sessions.Upsert(BotCLISession{BotID: botID, CLIType: result.resp.RuntimeType, SessionID: result.resp.SessionID, WorkDir: spec.WorkDir, UpdatedAt: now})`. Upsert failure is logged and does not fail the turn (the reply still goes out).

**Wiring in `bootstrap.go`:** construct `botCLISessionRepo := repositories.NewBotCLISessionRepository(db)` and pass it into both the resolver config and the orchestrator.

### 5. Workspace
- `internal/config`: `BotWorkspacePath(botID)` already exists — reuse it.
- `BotService.CreateBot`: set `Workspace: <computed default>` on the new `domain.Bot` (the service needs the configured workspace root; pass it in, or compute via a small helper given `botID`). Default = `config.BotWorkspacePath(botID)` semantics.
- `BotCLIResolver.Resolve`: `workDir := strings.TrimSpace(bot.Workspace)`; if empty, fall back to the existing computed `filepath.Join(workspaceRoot, botID, "workspace")`. `MkdirAll(workDir)` as today; set `spec.WorkDir = workDir`.

## Error handling
- No stored session ⇒ `ResumeSessionID` empty ⇒ driver passes no resume flag (fresh start) — exactly the requested behavior.
- Resume rejected by the CLI ⇒ driver starts fresh; the new session id is captured and overwrites the stored row on the next save.
- Session repo Get/Upsert errors are logged and never block a turn or a reply.
- Workspace `MkdirAll` failure is returned from `Resolve` as today.

## Testing
- **Repos:** `bot_cli_sessions` Upsert→Get round-trip (insert then update overwrites on the composite key); `bot.Workspace` create/read round-trip.
- **Resolver:** with a stored session for `(bot, capability.Key)` → `spec.ResumeSessionID` set; with none → empty; uses `bot.Workspace` when set, computed path when empty.
- **Drivers (per CLI):** `ResumeSessionID` set ⇒ the resume flag/behavior is applied (claude: `--resume <id>` present in args; opencode: reuse path taken; codex: resume call made); `Response.SessionID` captured. claude fully covered (deterministic `buildACPArgs` test + system/init capture); codex/opencode capture + best-effort resume covered against their fakes.
- **Orchestrator:** after a turn returning a `Response.SessionID`, the session repo receives an `Upsert` with `cli_type = RuntimeType`, the session id, and the work dir.

## Risks / notes
- Migrations MUST be appended as `000006` and `000007` (never renumbered) — same hazard as the earlier `bots.type` stranding incident.
- **codex/opencode cross-restart resume is best-effort.** opencode sessions are scoped to the opencode server process; a resumed id may not exist after a restart → fallback to fresh (acceptable, but means continuity is not guaranteed for opencode). The codex-acp resume call is confirmed against the live protocol during implementation; if the protocol has no resume, codex capture still works and resume is a no-op with the id stored for future use.
- `cli_type` alignment relies on `capability.Key == Response.RuntimeType`; both are the literal {claude,codex,opencode}. A test asserts this invariant so a future rename can't silently break the save/lookup pairing.
