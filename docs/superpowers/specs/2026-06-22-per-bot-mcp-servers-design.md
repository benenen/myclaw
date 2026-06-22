# Per-Bot MCP Server Attachment Design

## Goal
Let an operator maintain a **catalog** of MCP servers (`mcp1`, `mcp2`, …) and
**attach any subset of them to each bot**. When an orchestrator bot's agent CLI
launches, only that bot's attached (and globally-enabled) MCP servers are placed
into the `--mcp-config` JSON, alongside the built-in `myclaw` server. The agent
CLI itself connects http servers and spawns stdio servers from that config — the
service does not manage MCP processes.

## Context / Why this is a fresh build
An earlier attempt added a **global** MCP registry (every enabled server was
dumped into every orchestrator bot). That code was reverted (working tree clean,
DB rolled back to migration 7). This spec rebuilds the catalog layer from scratch
**and** adds the per-bot attachment layer, folding in three cleanups the review of
the reverted code surfaced.

## Scope

In scope:
- **Catalog** of MCP servers: `domain.MCPServer` entity + `MCPServerRepository`,
  GORM model, migration, an app-layer `mcpserver.Service` with validation, and
  `myclaw mcp <list|add|remove|enable|disable>` CLI.
- **Per-bot attachment**: a `bot_mcp_servers` join table (M:N), repository methods,
  `myclaw mcp <attach|detach>` + `mcp list --bot <id>` CLI, and a multi-select in
  the bot agent-config web card.
- **Resolver**: inject each orchestrator bot's enabled+attached servers into
  `--mcp-config` (replaces the reverted global dump).
- **API**: `GET /api/v1/mcp-servers` (catalog list for the UI); `ConfigureBotAgent`
  accepts `mcp_server_ids` (replace-set); bot responses echo `mcp_server_ids`.
- Tests for service, repository (incl. join + cascade), resolver, and handler.

Out of scope (v1):
- Catalog **create/remove** via web UI (UI only attaches/detaches existing catalog
  entries; catalog CRUD stays CLI-only).
- MCP for **non-orchestrator** bots (gating unchanged — see Decision 1).
- An `mcp update/edit` command (change a server = remove + re-add).
- Live process rewiring (changes take effect on the next agent launch).
- Spawning/health-checking MCP processes ourselves (the agent CLI does that).

## Decisions (locked during brainstorming)
1. **Scope = orchestrator bots only.** `--mcp-config` is still injected only inside
   the `bot.Role == domain.BotRoleOrchestrator` block in the resolver. Per-bot
   selection replaces the global "all enabled" dump for those bots; regular bots
   still get no MCP.
2. **Attachment surface = both CLI and web UI**, writing the same join table.
3. **Storage = a dedicated join table** `bot_mcp_servers(bot_id, mcp_server_id)`
   (chosen over a JSON `mcp_server_ids` column on `bots`): proper M:N, indexed,
   and makes "detach from all bots on server delete" a single statement.
4. **`enabled` is a global kill-switch.** A disabled catalog server is dropped from
   `--mcp-config` even if a bot has it attached; the attachment row is preserved.
5. **Replace-set semantics** for the web save and `SetBotServers`: the UI always
   sends the full selected id set, which replaces that bot's join rows.

## Background: how MCP config reaches the agent
`BotCLIResolver.Resolve` (`internal/app/bot/cli_resolver.go`) builds the
`agent.Spec`. Only when `bot.Role == domain.BotRoleOrchestrator` does it append
`--mcp-config <json>` (and `--append-system-prompt`). The JSON currently contains
just the built-in `myclaw` http server (`r.mcpURL`). The manager later picks a
driver by `spec.Type` and launches the CLI, which reads `--mcp-config` and
connects/spawns the listed servers.

## Architecture

### 1. Data model — migration `000008_mcp_servers.up.sql` (append-only; never renumber)
One migration introduces both tables (the feature ships together):
```sql
CREATE TABLE IF NOT EXISTS mcp_servers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    server_type TEXT NOT NULL DEFAULT 'http',
    url         TEXT NOT NULL DEFAULT '',
    command     TEXT NOT NULL DEFAULT '',
    args_json   TEXT NOT NULL DEFAULT '[]',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS bot_mcp_servers (
    bot_id        TEXT NOT NULL,
    mcp_server_id TEXT NOT NULL,
    created_at    DATETIME NOT NULL,
    PRIMARY KEY (bot_id, mcp_server_id)
);
CREATE INDEX IF NOT EXISTS idx_bot_mcp_servers_bot ON bot_mcp_servers(bot_id);
```
The redundant explicit `idx_mcp_servers_name` from the reverted version is dropped
(the `UNIQUE` on `name` already creates an index). `internal/store/db_test.go`
expects schema version **8**.

### 2. Domain (`internal/domain`)
- `entities.go`: `MCPServer{ID, Name, ServerType, URL, Command, Args []string,
  Enabled bool, CreatedAt, UpdatedAt}`.
- `repositories.go`: `MCPServerRepository` interface:
  ```
  Create(ctx, MCPServer) (MCPServer, error)
  GetByID(ctx, id) (MCPServer, error)
  GetByName(ctx, name) (MCPServer, error)
  List(ctx) ([]MCPServer, error)
  Update(ctx, MCPServer) (MCPServer, error)
  DeleteByID(ctx, id) error              // cascades: also deletes join rows
  // per-bot attachment
  ListByBot(ctx, botID) ([]MCPServer, error)         // all attached (any enabled)
  ListEnabledByBot(ctx, botID) ([]MCPServer, error)  // attached AND enabled
  SetBotServers(ctx, botID, serverIDs []string) error // replace-set
  AttachToBot(ctx, botID, serverID string) error      // idempotent
  DetachFromBot(ctx, botID, serverID string) error
  ```
  No standalone `ListEnabled` (the reverted global path is gone — no caller).

### 3. Store (`internal/store`)
- `models/mcp_server.go`: GORM model `MCPServer` (`args_json` text column,
  `Enabled *bool` with nil-guard on read). Plus a tiny `BotMCPServer` model
  (`bot_id`, `mcp_server_id`, `created_at`) → table `bot_mcp_servers`.
- `repositories/mcp_server_repository.go`:
  - Catalog CRUD as before (`Args` ↔ `args_json` JSON round-trip; `gorm` errors
    mapped to `domain.ErrNotFound`).
  - `DeleteByID` runs in a transaction: delete `bot_mcp_servers WHERE
    mcp_server_id = ?`, then delete the server; `RowsAffected == 0` on the server
    delete → `domain.ErrNotFound`.
  - `ListByBot` / `ListEnabledByBot`: join `bot_mcp_servers` to `mcp_servers`
    (`ListEnabledByBot` adds `enabled = 1`), `ORDER BY name`.
  - `SetBotServers`: transaction — delete this bot's rows, insert the given set
    (de-duplicated; `created_at = now`).
  - `AttachToBot`: insert ignoring duplicate (idempotent); `DetachFromBot`: delete.
  - `var _ domain.MCPServerRepository = (*MCPServerRepository)(nil)`.

### 4. App service (`internal/app/mcpserver/service.go`)
- Constants `TypeHTTP = "http"`, `TypeStdio = "stdio"`.
- `Service` holds `repo domain.MCPServerRepository` **and** `bots
  domain.BotRepository` (the latter for attach validation).
- `Create(ctx, CreateInput)`: validate type ∈ {http,stdio}, url required for http,
  command required for stdio. **Pre-check `GetByName`** and return a friendly
  `%w: server %q already exists` (`domain.ErrInvalidArg`) instead of leaking the
  raw SQLite UNIQUE error. Assign `domain.NewPrefixedID("mcp")`, `Enabled: true`.
- `List`, `Remove(name)` (resolve by name → `DeleteByID`, cascades), `SetEnabled`.
- `AttachToBot(ctx, botID, serverName)`: `bots.GetByID` (→ friendly not-found),
  `repo.GetByName` (→ friendly not-found), `repo.AttachToBot`.
- `DetachFromBot(ctx, botID, serverName)`: symmetric.
- `ListByBot(ctx, botID)`, `SetBotServers(ctx, botID, ids)` (validate each id
  exists, then replace-set).
- **Delete the reverted `config.go`** (`BuildMCPConfigJSON`) — it was dead code
  duplicating the resolver; the resolver is the single source of truth.

### 5. Resolver (`internal/app/bot/cli_resolver.go`)
- Keep `SetMCPServerRepository`.
- `buildMCPConfigJSON(ctx, botID)` (now takes `botID`):
  - seed `mcpServers["myclaw"] = {type:"http", url:r.mcpURL}` when `r.mcpURL != ""`.
  - if `r.mcpServers != nil`: `ListEnabledByBot(ctx, botID)`; for each, add
    `{type}` plus `url` (http) or `command`+`args` (stdio). On list error: log and
    fall back to just `myclaw` (non-fatal).
  - marshal `{"mcpServers": …}`.
- The call site stays inside the orchestrator block:
  `extra = append(extra, "--mcp-config", r.buildMCPConfigJSON(ctx, botID))`.

### 6. CLI (`cmd/mcp.go`)
- Keep `list` / `add` / `remove` / `enable` / `disable`. Remove the leftover
  `// ensure context is imported` comment.
- `newMCPService` also builds `repositories.NewBotRepository(db)` and passes it to
  `mcpserver.NewService(repo, botRepo)`.
- New `attach` / `detach`: flags `--bot <botID>` (required) and `--server <name>`
  (required) → `svc.AttachToBot` / `DetachFromBot`.
- `list --bot <botID>`: print that bot's attachments (`svc.ListByBot`); without the
  flag, behave as today (full catalog with enabled/disabled status).

### 7. HTTP API
- `dto.ConfigureBotAgentRequest`: add `MCPServerIDs []string`
  (`json:"mcp_server_ids,omitempty"`).
- `dto.BotResponse` (and the `BotListItem` it is built from): add `MCPServerIDs
  []string` (`json:"mcp_server_ids"`).
- `handlers.ConfigureBotAgent`: pass `req.MCPServerIDs` into the service input.
- New `handlers.ListMCPServers(deps.BotService)` → `GET /api/v1/mcp-servers`
  (mirrors `ListAgentCapabilities`); register in `router.go`. Returns `[{id, name,
  server_type, enabled}]`.
- `dto`: a `MCPServerResponse{ID, Name, ServerType, Enabled}` for the list.

### 8. App wiring for the API path (`internal/app/bot/service.go`)
- `NewBotService(... , mcpServers domain.MCPServerRepository)` — add the repo.
- `ConfigureBotAgent`: after persisting capability/mode/alias, call
  `s.mcpServers.SetBotServers(ctx, bot.ID, input.MCPServerIDs)`; populate the
  returned `BotListItem.MCPServerIDs` from `ListByBot`.
- `ListBots`: populate each item's `MCPServerIDs` from `ListByBot`.
- New `ListMCPServers(ctx)` → `[]MCPServerListItem` for the new endpoint.

### 9. Bootstrap (`internal/bootstrap/bootstrap.go`)
- `mcpServerRepo := repositories.NewMCPServerRepository(db)`.
- Pass `mcpServerRepo` into `NewBotService(...)`.
- `resolver.SetMCPServerRepository(mcpServerRepo)`.

### 10. Frontend (`internal/api/http/web/static/`)
- On bot-detail render, `GET /api/v1/mcp-servers` once; render a labelled
  multi-select (or checkbox list) in the agent-config card, pre-checking the bot's
  `mcp_server_ids`. Disabled catalog servers may be shown greyed/annotated.
- `saveSelectedBotAgent` includes `mcp_server_ids` (array of selected ids) in the
  `ConfigureBotAgent` POST; on success, update the local `bot.mcp_server_ids`.

## Error handling
- Create with a duplicate name → friendly `ErrInvalidArg` ("already exists"),
  surfaced by the CLI as a clean message (no raw driver string).
- `attach`/`detach` with an unknown bot or server name → `ErrNotFound`, friendly
  CLI message naming what was missing.
- `SetBotServers` with an unknown server id → `ErrInvalidArg` (reject the whole
  save; the UI only ever sends ids it fetched, so this guards tampering/races).
- Resolver: a failed `ListEnabledByBot` is non-fatal — log and inject only the
  built-in `myclaw` server (a bad DB read must not break agent launch).
- `attach` is idempotent (re-attaching the same server is a no-op success).

## Testing
- **Repository:** catalog CRUD round-trip incl. `Args`; duplicate-name insert
  errors; `AttachToBot` idempotent; `SetBotServers` replace-set (adds + removes);
  `ListEnabledByBot` excludes disabled and unattached; `DeleteByID` cascades to
  `bot_mcp_servers`.
- **Service:** validation matrix (type/url/command); friendly duplicate-name;
  attach/detach validate bot + server existence; `Remove` cascades.
- **Resolver:** orchestrator bot with two attached servers (one disabled) → JSON
  contains `myclaw` + the one enabled server, http vs stdio shapes correct;
  non-orchestrator bot → no `--mcp-config`; nil repo → only `myclaw`; list error →
  only `myclaw`.
- **Handler:** `mcp_server_ids` in `ConfigureBotAgent` persists and is echoed back;
  empty array clears attachments; `GET /api/v1/mcp-servers` returns the catalog.

## Risks / notes
- **Migration is append-only `000008`.** Never insert mid-sequence or renumber —
  that is exactly the stranded-DB hazard that just produced the version-8/7
  mismatch during the revert. `db_test.go` asserts version 8.
- No FK constraints (SQLite + GORM convention here), so cascade cleanup lives in
  `DeleteByID`'s transaction. A bot deleted elsewhere can leave orphan join rows;
  `ListEnabledByBot` only reads rows for the queried bot, so orphans are inert.
  (A future bot-delete cascade is out of scope for v1.)
- Changing a bot's attachments takes effect on its next agent launch/session,
  consistent with how capability/mode/alias changes already apply.
