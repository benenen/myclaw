# Per-bot agent environment variables

## Goal
Let each bot carry its own set of **agent environment variables**, stored in the
DB and editable via API + Web UI. At agent launch the resolver injects them into
the spawned agent process and logs them. Mirrors the existing per-bot system-prompt
feature in shape.

## Background — the runtime is already wired
`agent.Spec` already has `Env map[string]string`, and **every driver** already
merges it onto the inherited environment:
`cmd.Env = append(os.Environ(), flattenEnv(spec.Env)...)`
(`internal/agent/codex/driver_acp.go`, `driver_exec.go`, `claude/driver_acp.go`,
`opencode/driver_acp.go`). `session.go` clones `spec.Env`. So this feature only has
to populate `spec.Env` from the bot — no driver changes.

## Decisions (locked)
1. **Storage:** a JSON column `agent_env` on `bots` (same approach as system_prompt's
   column); `map[string]string`.
2. **Injection:** `spec.Env = bot.AgentEnv`. Drivers already append it onto
   `os.Environ()`, so bot env **overlays** (does not replace) the inherited env.
3. **Logging:** the resolver logs the injected env **in full `KEY=VALUE` form** at
   launch (operator explicitly wants to see values). Logged only when non-empty.
   (Caveat acknowledged: values may be secrets and land in logs; this is the chosen
   behavior for this self-hosted operator tool.)
4. **UI:** a key-value rows editor (KEY input | VALUE input | remove ×, + add) in the
   merged agent-config card.

## Data model
- **Migration** `internal/store/migrations/000010_bot_agent_env.up.sql` (up-only, like
  the others):
  `ALTER TABLE bots ADD COLUMN agent_env TEXT NOT NULL DEFAULT '{}';`
- **GORM model** `internal/store/models/bot.go`: add
  `AgentEnv map[string]string `gorm:"serializer:json;column:agent_env;not null;default:'{}'"``
  (GORM's `serializer:json` marshals the map to the TEXT column; keeps the bot repo's
  1:1 field mapping). If `serializer:json` does not round-trip cleanly on this
  GORM/SQLite, fall back to a `AgentEnvJSON string` column + manual
  `json.Marshal`/`Unmarshal` in the repo (the `mcp_servers.args_json` pattern) — the
  test in the plan must catch this.
- **Domain entity** `internal/domain/entities.go` `Bot`: add `AgentEnv map[string]string`.
- **Repository** `internal/store/repositories/bot_repository.go`: carry `AgentEnv` in
  the `models.Bot{…}` literals in `Create` + `Update`, and in `toDomainBot`.

## Resolver (inject + log)
`internal/app/bot/cli_resolver.go`, in `Resolve` (e.g. right after the system-prompt
doc write, before the orchestrator block):
```go
if len(bot.AgentEnv) > 0 {
    spec.Env = bot.AgentEnv
    log.Printf("agent launch env: bot_id=%s %s", botID, formatEnvKV(bot.AgentEnv))
}
```
Helper (sorted for deterministic output; full KEY=VALUE):
```go
func formatEnvKV(env map[string]string) string {
    keys := make([]string, 0, len(env))
    for k := range env { keys = append(keys, k) }
    slices.Sort(keys)
    parts := make([]string, 0, len(keys))
    for _, k := range keys { parts = append(parts, k+"="+env[k]) }
    return strings.Join(parts, " ")
}
```
(`slices`, `strings`, `log` already imported in the resolver.) When `AgentEnv` is
empty, `spec.Env` stays nil and drivers skip env injection (unchanged).

## API (mirror system_prompt plumbing)
- **Service** `internal/app/bot/service.go`: add `AgentEnv map[string]string` to
  `CreateBotInput`, `CreateBotOutput`, `BotListItem`, `ConfigureBotAgentInput`.
  `CreateBot` persists+returns it; `ListBots` maps it; `ConfigureBotAgent` sets
  `bot.AgentEnv = input.AgentEnv` (normalize nil → `map[string]string{}`) before
  `Update` and returns it.
- **DTO** `internal/api/http/dto/bots.go`: add
  `AgentEnv map[string]string `json:"agent_env,omitempty"`` to `CreateBotRequest`,
  `CreateBotResponse`, `BotResponse` (= `ConfigureBotAgentResponse` alias — covers
  configure + list), `ConfigureBotAgentRequest`.
- **Handlers** `internal/api/http/handlers/bots.go`: plumb `agent_env` through
  `CreateBot` (in+out), `ListBots` (out), `ConfigureBotAgent` (in+out).

## Web UI — key-value rows editor
`internal/api/http/web/static/{index.html,app.js,style.css}`:
- In the merged agent-config card, add an **「环境变量 / Env」** section: a container
  `#detail-agent-env-rows` (one row per var = a `KEY` text input, a `VALUE` text
  input, and a `×` remove button) plus a `+ 添加` button below.
- `app.js`:
  - `renderEnvRows(envObj)` — clear `#detail-agent-env-rows` and append one row per
    entry of `envObj` (empty object → no rows). A helper `addEnvRow(k, v)` builds a row.
  - `collectEnv()` — read every row → `{KEY: VALUE}`, skipping rows whose trimmed KEY
    is empty. Used by BOTH `saveSelectedBotAgent` and the system-prompt modal's
    `saveSystemPrompt` (both send the full config; add `agent_env: collectEnv()`).
  - In `renderSelectedBotAgentControls`: `renderEnvRows(selectedBot()?.agent_env || {})`.
  - After a successful save, update `selectedBot().agent_env` from the response.
- `style.css`: `.env-row` (flex row: KEY ~40%, VALUE ~grow, × button), `.env-rows`
  container, `+ 添加` button — match the dark theme; reuse input styling.

## Error handling
- Empty env → `spec.Env` nil, no log, drivers skip (unchanged path).
- Repo/DB errors propagate as today. nil map normalized to `{}` in the service so the
  DB never stores a bare `null`.
- UI: rows with empty KEY are dropped on collect; duplicate KEYs — last wins (map).

## Testing
- **Repo round-trip:** create/configure a bot with `agent_env={"A":"1","B":"two"}`;
  read back (model↔domain) and assert equality; configure to `{}` and assert cleared.
  (This test also validates the `serializer:json` choice.)
- **Resolver:** a bot with `AgentEnv` set → `spec.Env` equals it; a bot with empty
  `AgentEnv` → `spec.Env` is nil/empty. (`formatEnvKV` covered by a small unit test:
  sorted `KEY=VALUE` joined by spaces.)
- **Service:** `ConfigureBotAgent` with `AgentEnv` persists + returns it (trim/normalize).
- **Handler:** configure (and create) round-trips `agent_env` via `BotResponse`.
- **UI:** API smoke + manual (no JS test harness).

## Out of scope (v1)
- Per-CLI env differences (one env map per bot, injected for whichever CLI launches).
- Secret masking / encryption of env values (stored + logged in plaintext by choice).
- Validating/normalizing KEY names (POSIX env-name rules) — operator's responsibility.
- Importing a `.env` file / bulk paste (key-value rows only).

## Notes
- Branch `feat/per-bot-agent-env` off `main`. No driver changes (runtime already
  wired). No data migration (new column defaults `{}`; existing bots get empty env).
- Implement in an isolated worktree (the live service is air-watched); merge to deploy.
  go:embed UI changes need a rebuild — the merge + air re-embed handles it.
