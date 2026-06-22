# Per-bot system prompt → workspace doc file (AGENTS.md / CLAUDE.md)

## Goal
Let each bot carry its own **system prompt**, stored in the DB and editable via API +
Web UI. At agent launch, the resolver materializes that prompt into the bot's
workspace as the CLI's native instruction file — `AGENTS.md` for codex/opencode,
`CLAUDE.md` for claude — so the launched agent picks it up automatically. This
turns today's manual "hand-write AGENTS.md into a workspace" hack into a managed,
per-bot, DB-backed feature.

## Background
- The CLI resolver (`internal/app/bot/cli_resolver.go`) already computes each bot's
  launch `agent.Spec`, creates `spec.WorkDir` (`<workspaceRoot>/<botID>/workspace`)
  via `os.MkdirAll`, and knows the CLI via `capability.Key` (`codex` / `claude` /
  `opencode`).
- Verified empirically (codex 0.137.0): codex reads `AGENTS.md` from its working
  directory and obeys it. claude reads `CLAUDE.md`; opencode reads `AGENTS.md`.
  codex has **no** `--append-system-prompt` flag (it rejects unknown flags), so a
  file is the only injection path for codex — hence the uniform file-based design.
- This is **orthogonal** to the orchestrator prompt (`--append-system-prompt`,
  injected only for `role == orchestrator`, claude-only). The two stack and are not
  merged by this feature.

## Decisions (locked)
1. **Uniform file-based injection.** `capability.Key == "claude"` → `CLAUDE.md`;
   otherwise (codex/opencode/unknown) → `AGENTS.md`.
2. **myclaw fully owns the file** (the file is a generated artifact; the DB is the
   single source of truth): each launch overwrites it. **Empty prompt ⇒ delete the
   file.**
3. **Full scope this iteration:** DB column + API + resolver write + Web UI textarea.

## Data model
Add `SystemPrompt` to the bot, end to end:
- **Migration** `internal/store/migrations/000009_bot_system_prompt.up.sql`:
  `ALTER TABLE bots ADD COLUMN system_prompt TEXT NOT NULL DEFAULT '';`
  `.down.sql`: `ALTER TABLE bots DROP COLUMN system_prompt;` (SQLite ≥ 3.35; match
  the style of the existing `000008` down migration).
- **GORM model** `internal/store/models/bot.go`: add
  `SystemPrompt string `gorm:"not null;default:'';column:system_prompt"``.
- **Domain entity** `internal/domain/entities.go` `Bot`: add `SystemPrompt string`.
- **Repository** `internal/store/repositories/bot_repository.go`: add `SystemPrompt`
  to the `models.Bot{…}` literal in `Create`, to the `toDomainBot` mapper, and to the
  update/configure write path (the method `ConfigureBotAgent` calls to persist).

## API
The system prompt is part of **agent configuration**, so it travels with the
existing configure-agent path (alongside capability/mode/alias/mcp):
- **DTO** `internal/api/http/dto/bots.go`:
  - `ConfigureBotAgentRequest`: add `SystemPrompt string `json:"system_prompt,omitempty"``.
  - `BotResponse`: add `SystemPrompt string `json:"system_prompt,omitempty"`` (so the
    UI can display/edit the current value).
  - `CreateBotRequest` / `CreateBotResponse`: add the same optional field (a bot may
    be created with a prompt; empty if omitted).
- **Service** `internal/app/bot/service.go`: add `SystemPrompt` to `CreateBotInput`
  and to `ConfigureBotAgentInput`; persist it on the bot. The returned `BotListItem`
  (and create output / `Get`) carry it back so handlers can surface it.
- **Handlers** `internal/api/http/handlers/bots.go`: plumb the field through
  create / configure / response mapping.

## Resolver behavior (the file write)
In `BotCLIResolver.Resolve`, after `spec.WorkDir` is set and `os.MkdirAll(spec.WorkDir)`
succeeds (so only when there IS a workspace), add a step:
```
docFile := "AGENTS.md"
if capability.Key == "claude" { docFile = "CLAUDE.md" }
path := filepath.Join(spec.WorkDir, docFile)
if strings.TrimSpace(bot.SystemPrompt) != "" {
    if err := os.WriteFile(path, []byte(bot.SystemPrompt), 0o644); err != nil {
        return agent.Spec{}, err          // fatal: same severity as MkdirAll failure
    }
} else {
    if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
        return agent.Spec{}, err          // empty prompt ⇒ remove; missing is fine
    }
}
```
Notes:
- File content is the **raw prompt text only** — no generated-header comment (keeps
  the prompt clean).
- Write/remove failure is **fatal to Resolve** (returns error) so the message
  handler reports failure rather than silently launching a prompt-less / stale
  agent. A missing file on the delete path is not an error.
- Only runs when `spec.WorkDir != ""` (a bot with no workspace gets no file — there
  is nowhere to put it; unchanged from today).

## Web UI
`internal/api/http/web/static/index.html` + `app.js`:
- Add a **「系统提示词 / System Prompt」 `<textarea>`** to the bot agent-config form
  (the same form that sets capability/mode/alias/MCP).
- On submit, include `system_prompt` in the configure (and/or create) payload.
- When opening an existing bot for edit, prefill the textarea from
  `BotResponse.system_prompt`.
- Empty textarea ⇒ send empty string ⇒ backend deletes the file. (Match the form's
  existing field-wiring patterns; the implementer reads `app.js` first.)

## Error handling
- DB: standard repo errors propagate (create/configure).
- Resolver file write or remove failure → `Resolve` returns the error → existing
  `processMessage` path replies with `failedReply` and logs (no new surface needed).
- No workspace → no file operation (skip).

## Testing
- **Repo round-trip:** create/configure a bot with `system_prompt`; read it back
  (domain + model mapping) and assert it persists; configure to empty and assert
  cleared.
- **Resolver** (table/scenario tests using a real `t.TempDir()` workspace + a stub
  bot repo returning a bot with `Workspace` set):
  - codex bot + non-empty prompt → `<workspace>/AGENTS.md` exists with exact prompt
    bytes.
  - claude bot + non-empty prompt → `<workspace>/CLAUDE.md` with exact bytes.
  - empty prompt + pre-existing doc file → file removed; `Resolve` ok.
  - empty prompt + no file → `Resolve` ok (no error).
  - pre-existing doc file + new prompt → overwritten with new bytes.
- **Handler:** configure-agent (and create) carries `system_prompt` in → out via
  `BotResponse`.
- **Web UI:** API smoke + manual (no JS test harness assumed).

## Rollout / migration
After this ships, move the **hand-written routing `AGENTS.md`** currently in
shiben's workspace into `shiben.system_prompt` (via the configure endpoint), so the
resolver manages it. Until that value is set, shiben's `system_prompt` is empty and
the next launch would **delete** the hand-written file — so the DB value must be set
as part of rollout (same content → file rewritten identically).

## Out of scope (v1)
- Per-CLI prompt variants on one bot (one prompt, file chosen by the bot's CLI).
- Templating / variable substitution in the prompt.
- Merging with the orchestrator `--append-system-prompt` (kept orthogonal).
- A generated-header/managed-marker block (rejected in favor of full-own overwrite).
- Versioning / history of prompt edits.

## Notes
- Branch `feat/per-bot-system-prompt` off `main`. Implementation runs in an isolated
  worktree (the live service is air-watched on the main checkout — half-built code in
  the main dir would auto-deploy), then merge to main to deploy.
- No new dependencies (stdlib `os`, `path/filepath`, `strings`, `errors` already in
  the resolver).
