# Per-Bot CLI Alias Design

## Goal
Let a user give a bot a custom **CLI command (alias)** in the web UI. When that bot's agent CLI launches, the system executes the alias instead of the auto-detected default command — e.g. a bot configured for `codex` can be launched as `cx`. The alias is per-bot, overrides only the executable, bypasses the availability probe, and still behaves as the selected CLI (real-binary args injected).

## Scope

In scope:
- New per-bot `CLIAlias` field (entity + GORM model + DB migration).
- Resolver change: when a bot has an alias, override the launch command, skip the `Available` gate, and mark the spec as a real CLI.
- A new `agent.Spec.RealCLI` flag so the CLI drivers inject real-binary args even when the command basename isn't the canonical name.
- Driver change (codex acp+exec, claude acp, opencode acp): treat the command as the real binary when `RealCLI` is set.
- API: extend the existing per-bot agent-config endpoint (`ConfigureBotAgent`) to accept `cli_alias`.
- UI: an optional "CLI alias" input in the bot's agent-config card.
- Tests for resolver, drivers, and handler.

Out of scope:
- Alias as a full "command + args" string (explicitly rejected — alias is the executable only).
- Per-CLI/global aliases (decided per-bot).
- Changing detection/discovery to probe for alias names.
- A separate alias-management screen (reuse the agent-config card).

## Decisions (locked during brainstorming)
1. **Scope:** per-bot (the alias lives on the bot, not on the capability).
2. **Semantics:** alias overrides the executable command. Launch does `exec <alias>` plus the selected CLI's normal args.
3. **Detection:** setting an alias **bypasses** the capability `Available` check (the real binary may be named `cx`, so `codex` detection would fail).
4. **Identity preserved:** the bot still selects a capability + mode; the alias only swaps the executable and is still treated as the selected CLI for arg injection.
5. **Driver arg-injection (approach A):** add an explicit `RealCLI` flag to the spec rather than refactoring all basename sniffing (approach B was the larger, cleaner-but-out-of-scope alternative).

## Background: how CLI launch works today
- `domain.AgentCapability{Key, Command, Args, SupportedModes, Available, ...}` is one detected CLI (claude/codex/opencode). The discoverer seeds it and resolves `Command` to a path, setting `Available`.
- `BotCLIResolver.Resolve` (`internal/app/bot/cli_resolver.go`) builds an `agent.Spec` for a bot: it requires `AgentCapabilityID` + `AgentMode`, rejects unavailable capabilities (`!capability.Available → ErrBotCLIUnavailable`), and sets `spec.Command = capability.Command`, `spec.Args = capability.Args`, `spec.Type = bot.AgentMode`.
- The manager picks a driver by `spec.Type` (the mode). The chosen CLI driver then does `base := filepath.Base(spec.Command)` and uses `base == "codex"` / `isClaudeCommand` / `base == "opencode"` to decide whether to inject the real-binary ACP args (vs. passing args verbatim to a test stub).

The alias breaks step 3's basename check, which is why `RealCLI` is needed.

## Architecture

### 1. Data model
- `domain.Bot`: add `CLIAlias string`.
- `internal/store/models/bot.go`: add the GORM column mapping (`cli_alias`).
- Migration **`000005_bot_cli_alias.up.sql`** (APPEND ONLY — do not insert mid-sequence; a renumbered migration is exactly the stranded-DB bug just fixed in `bots.type`):
  ```sql
  ALTER TABLE bots ADD COLUMN cli_alias TEXT NOT NULL DEFAULT '';
  ```
- Repository read/write paths already round-trip the whole `Bot`; add the field to any explicit field lists in the bot repository if present.

### 2. Spec flag
- `agent.Spec`: add `RealCLI bool`. Default `false` preserves current behavior (test stubs still receive args verbatim).

### 3. Resolver (`BotCLIResolver.Resolve`)
When `bot.CLIAlias != ""`:
- Skip the `!capability.Available` rejection and the `capability.Command == ""` rejection.
- Set `spec.Command = bot.CLIAlias`.
- Set `spec.RealCLI = true`.

When `bot.CLIAlias == ""`: unchanged (current gating + `spec.Command = capability.Command`, `spec.RealCLI = false`).

The capability + mode are still required and validated (mode must be in `SupportedModes`); the alias only overrides the executable and the availability gate. Base `capability.Args` are still applied.

### 4. Drivers
In each driver, change the real-binary test from a bare basename check to "basename matches OR `spec.RealCLI`":
- `internal/agent/codex/driver_acp.go` (the `base != "codex"` check) and `driver_exec.go` if it has an equivalent.
- `internal/agent/claude/driver_acp.go` (`isClaudeCommand` usage — gate the arg injection on `isClaudeCommand(cmd) || spec.RealCLI`).
- `internal/agent/opencode/driver_acp.go` (the `base != "opencode"` check).

The driver is already CLI-specific (the codex driver injects codex's args), so `RealCLI` only flips the "is this the real binary" gate; it does not change *which* args are injected.

### 5. API
- `dto.ConfigureBotAgentRequest`: add `CLIAlias string` (`json:"cli_alias,omitempty"`).
- `handlers.ConfigureBotAgent`: pass it into the service call.
- Bot service agent-config method: persist `CLIAlias` onto the bot (alongside `AgentCapabilityID`/`AgentMode`). Trim whitespace; empty string clears the alias.
- `dto.BotResponse` / list + create responses: include `cli_alias` so the UI can render the current value.

### 6. UI (`internal/api/http/web/static/`)
- In the bot detail's agent-config card (where capability + mode are chosen and Saved), add an optional text input **"CLI alias"** with placeholder `e.g. cx`, pre-filled from the bot's `cli_alias`.
- The existing Save action (`saveSelectedBotAgent`) includes `cli_alias` in its `ConfigureBotAgent` POST.

### 7. Lifecycle
The alias takes effect on the next CLI launch / session (consistent with how capability/mode changes apply today). No live process is rewired.

## Error handling
- Empty alias = "no override" (default, current behavior).
- A non-empty alias that doesn't resolve to an executable at launch surfaces as the normal CLI-start failure (the driver/exec returns an error → bot connection error), same path as a missing binary today. No new pre-validation in v1.
- Mode validation against `SupportedModes` still applies even with an alias.

## Testing
- **Resolver:** alias set → `spec.Command == alias`, `spec.RealCLI == true`, available-gate bypassed even when `capability.Available == false`; alias empty → unchanged (`spec.Command == capability.Command`, `RealCLI == false`, unavailable still errors). Mode validation still enforced.
- **Drivers:** with `RealCLI == true` and a non-canonical basename (`cx`), real-binary args are injected; with `RealCLI == false` and a stub basename, args are passed verbatim (existing behavior preserved).
- **Handler:** `cli_alias` in `ConfigureBotAgent` persists to the bot and is echoed back in the bot response; empty clears it.

## Risks / notes
- The migration MUST be appended as `000005` and never inserted earlier — re-living the renumbering hazard would strand existing DBs.
- The UI capability dropdown may currently filter to `Available` capabilities; with aliases, a user may want to pick a capability whose canonical binary isn't detected. If the dropdown hides unavailable capabilities, the plan should allow selecting them (or note that the capability must still appear). This is an implementation detail to confirm during planning.
