# Per-bot Agent Env Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each bot a DB-stored `agent_env` (map of env vars) that the resolver injects into the spawned agent process and logs at launch, editable via API + a Web UI key-value editor.

**Architecture:** Add an `agent_env` JSON column on `bots` (GORM `serializer:json`), flow it through service/DTO/handlers like `system_prompt`, set `spec.Env = bot.AgentEnv` in the resolver (drivers already append it onto `os.Environ()`), log it `KEY=VALUE`, and add a key-value rows editor in the merged agent card.

**Tech Stack:** Go 1.23, GORM + SQLite (up-only embedded migrations), `net/http`, vanilla JS/HTML static UI (`go:embed`).

**Spec:** `docs/superpowers/specs/2026-06-23-per-bot-agent-env-design.md`. Branch `feat/per-bot-agent-env`. No driver changes. Implement in an isolated worktree; merge to deploy (go:embed UI needs a rebuild).

---

## Task 1: DB column + model + domain + repo + db_test bump

**Files:** Create `internal/store/migrations/000010_bot_agent_env.up.sql`; modify `internal/store/models/bot.go`, `internal/domain/entities.go`, `internal/store/repositories/bot_repository.go`, `internal/store/db_test.go`; test `internal/store/repositories/bot_repository_test.go`.

- [ ] **Step 1: Write the failing test** — append to `bot_repository_test.go`:
```go
func TestBotRepositoryAgentEnvRoundTrip(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.Bot{
		ID: "bot_env", UserID: "usr_1", Name: "envy", ChannelType: "feishu",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
		AgentEnv:         map[string]string{"A": "1", "B": "two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AgentEnv["A"] != "1" || created.AgentEnv["B"] != "two" {
		t.Fatalf("create agent_env = %+v", created.AgentEnv)
	}
	got, err := repo.GetByID(ctx, "bot_env")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentEnv["A"] != "1" || got.AgentEnv["B"] != "two" {
		t.Fatalf("get agent_env = %+v", got.AgentEnv)
	}
	got.AgentEnv = map[string]string{}
	updated, err := repo.Update(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.AgentEnv) != 0 {
		t.Fatalf("after clear agent_env = %+v", updated.AgentEnv)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`domain.Bot` has no `AgentEnv`): `go test ./internal/store/repositories/ -run TestBotRepositoryAgentEnvRoundTrip`

- [ ] **Step 3: Add the migration + fields**

`internal/store/migrations/000010_bot_agent_env.up.sql` (up-only):
```sql
ALTER TABLE bots ADD COLUMN agent_env TEXT NOT NULL DEFAULT '{}';
```
`internal/store/models/bot.go` — add after the `SystemPrompt` field:
```go
	AgentEnv map[string]string `gorm:"serializer:json;column:agent_env;not null;default:'{}'"`
```
`internal/domain/entities.go` — in `type Bot struct`, add after `SystemPrompt string`:
```go
	AgentEnv          map[string]string
```
`internal/store/repositories/bot_repository.go` — add `AgentEnv: bot.AgentEnv,` to the `models.Bot{…}` literal in **both** `Create` and `Update` (after `SystemPrompt`), and add `AgentEnv: m.AgentEnv,` to `toDomainBot` (after `SystemPrompt`).

> If `serializer:json` does not round-trip on this GORM/SQLite (test still fails after this), switch to the manual pattern used by `mcp_servers.args_json`: model field `AgentEnvJSON string `gorm:"column:agent_env"``, and `json.Marshal`/`Unmarshal` between it and `domain.Bot.AgentEnv` inside `Create`/`Update`/`toDomainBot`. Then re-run the test.

- [ ] **Step 4: Bump the schema-version assertions**

`internal/store/db_test.go` has two `if version != 9` checks (in `TestMigrateCreatesCoreTables` and `TestMigrateIsIdempotent`) — change both `9` → `10` (every new migration bumps this).

- [ ] **Step 5: Run — expect PASS**: `go test ./internal/store/... -run 'TestBotRepositoryAgentEnvRoundTrip|TestMigrate' -v`

- [ ] **Step 6: Commit**
```bash
git add internal/store/migrations/000010_bot_agent_env.up.sql internal/store/models/bot.go internal/domain/entities.go internal/store/repositories/bot_repository.go internal/store/db_test.go internal/store/repositories/bot_repository_test.go
git commit -m "feat(bot): add agent_env column (migration, model, domain, repo)"
```
(Trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.)

---

## Task 2: Service + DTO + handlers plumbing

**Files:** Modify `internal/app/bot/service.go`, `internal/api/http/dto/bots.go`, `internal/api/http/handlers/bots.go`; test `internal/app/bot/service_test.go`.

- [ ] **Step 1: Write the failing test** — append to `service_test.go`:
```go
func TestBotServiceConfigureBotAgentEnv(t *testing.T) {
	svc := newTestBotService(t)
	created, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_env", Name: "envy", ChannelType: "feishu",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.ConfigureBotAgent(context.Background(), ConfigureBotAgentInput{
		BotID: created.BotID, AgentCapabilityID: "cap_codex", AgentMode: "codex-exec",
		AgentEnv: map[string]string{"TOKEN": "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentEnv["TOKEN"] != "abc" {
		t.Fatalf("returned agent_env = %+v", updated.AgentEnv)
	}
	stored, err := svc.bots.GetByID(context.Background(), created.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AgentEnv["TOKEN"] != "abc" {
		t.Fatalf("stored agent_env = %+v", stored.AgentEnv)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`ConfigureBotAgentInput`/`BotListItem` have no `AgentEnv`): `go test ./internal/app/bot/ -run TestBotServiceConfigureBotAgentEnv`

- [ ] **Step 3: Add `AgentEnv` to service inputs/outputs**

In `internal/app/bot/service.go`, add `AgentEnv map[string]string` right after each existing `SystemPrompt` field/line:
- `CreateBotInput` (after `SystemPrompt string`)
- `CreateBotOutput` (after `SystemPrompt string`)
- `BotListItem` (after `SystemPrompt string`)
- `ConfigureBotAgentInput` (after `SystemPrompt string`)
- `CreateBot`: in the `domain.Bot{…}` passed to `s.bots.Create`, add `AgentEnv: input.AgentEnv,`; in the returned `CreateBotOutput{…}`, add `AgentEnv: bot.AgentEnv,`.
- `ListBots`: in the appended `BotListItem{…}`, add `AgentEnv: bot.AgentEnv,`.
- `ConfigureBotAgent`: after `bot.SystemPrompt = strings.TrimSpace(input.SystemPrompt)`, add:
  ```go
  bot.AgentEnv = input.AgentEnv
  if bot.AgentEnv == nil {
  	bot.AgentEnv = map[string]string{}
  }
  ```
  and in the returned `BotListItem{…}`, add `AgentEnv: bot.AgentEnv,`.

- [ ] **Step 4: Add `agent_env` to the DTOs**

In `internal/api/http/dto/bots.go` add `AgentEnv map[string]string `json:"agent_env,omitempty"`` after each existing `SystemPrompt` line, in: `CreateBotRequest`, `CreateBotResponse`, `BotResponse` (= `ConfigureBotAgentResponse`), `ConfigureBotAgentRequest`.

- [ ] **Step 5: Plumb the handlers**

In `internal/api/http/handlers/bots.go`:
- `CreateBot`: add `AgentEnv: req.AgentEnv,` to `botapp.CreateBotInput{…}`, and `AgentEnv: result.AgentEnv,` to `dto.CreateBotResponse{…}`.
- `ListBots`: add `AgentEnv: item.AgentEnv,` to the `dto.BotResponse{…}` literal.
- `ConfigureBotAgent`: add `AgentEnv: req.AgentEnv,` to `botapp.ConfigureBotAgentInput{…}`, and `AgentEnv: result.AgentEnv,` to `dto.ConfigureBotAgentResponse{…}`.

- [ ] **Step 6: Run — expect PASS** + build: `go test ./internal/app/bot/ -run TestBotServiceConfigureBotAgentEnv -v && go build ./...`

- [ ] **Step 7: Commit**
```bash
git add internal/app/bot/service.go internal/app/bot/service_test.go internal/api/http/dto/bots.go internal/api/http/handlers/bots.go
git commit -m "feat(bot): carry agent_env through service, DTOs, handlers"
```

---

## Task 3: Resolver injects + logs the env

**Files:** Modify `internal/app/bot/cli_resolver.go`; test `internal/app/bot/cli_resolver_test.go`.

- [ ] **Step 1: Write the failing tests** — append to `cli_resolver_test.go`:
```go
func TestResolveInjectsAgentEnv(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_e", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp",
		AgentEnv: map[string]string{"FOO": "bar"},
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_e")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.Env["FOO"] != "bar" {
		t.Fatalf("spec.Env = %+v", spec.Env)
	}
}

func TestFormatEnvKVSortedKeyValue(t *testing.T) {
	got := formatEnvKV(map[string]string{"B": "2", "A": "1"})
	if got != "A=1 B=2" {
		t.Fatalf("formatEnvKV = %q", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`formatEnvKV` undefined; `spec.Env` unset): `go test ./internal/app/bot/ -run 'TestResolveInjectsAgentEnv|TestFormatEnvKV'`

- [ ] **Step 3: Implement in `cli_resolver.go`**

In `Resolve`, right after the `if spec.WorkDir != "" { … writeSystemPromptDoc … }` block, add:
```go
	if len(bot.AgentEnv) > 0 {
		spec.Env = bot.AgentEnv
		log.Printf("agent launch env: bot_id=%s %s", botID, formatEnvKV(bot.AgentEnv))
	}
```
Add this helper (e.g. just below `writeSystemPromptDoc`):
```go
// formatEnvKV renders env as sorted "KEY=VALUE" pairs (logged at launch).
func formatEnvKV(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}
	return strings.Join(parts, " ")
}
```
(`slices`, `strings`, `log` are already imported.)

- [ ] **Step 4: Run — expect PASS** (+ whole bot package): `go test ./internal/app/bot/ -v`

- [ ] **Step 5: Commit**
```bash
git add internal/app/bot/cli_resolver.go internal/app/bot/cli_resolver_test.go
git commit -m "feat(bot): resolver injects + logs per-bot agent env at launch"
```

---

## Task 4: Web UI — key-value env editor

**Files:** Modify `internal/api/http/web/static/index.html`, `internal/api/http/web/static/app.js`, `internal/api/http/web/static/style.css`. (Manual + serve-check; no JS harness.)

- [ ] **Step 1: Add the env section to the agent card**

In `index.html`, inside the merged agent card's `form-grid`, **between** the MCP servers field and the `系统提示词` button field, insert:
```html
              <div class="form-field wide">
                <label>环境变量 / Env</label>
                <div class="env-rows" id="detail-agent-env-rows"></div>
                <button type="button" class="btn-secondary env-add" onclick="addEnvRow('', '')">+ 添加</button>
              </div>
```

- [ ] **Step 2: app.js — render / add / collect**

Add these functions (near `renderMCPOptions`/`selectedMCPServerIds`):
```js
function addEnvRow(k, v) {
  const rows = document.getElementById('detail-agent-env-rows');
  const row = document.createElement('div');
  row.className = 'env-row';
  row.innerHTML = `
    <input type="text" class="env-key" placeholder="KEY" value="${escapeHtml(k || '')}">
    <input type="text" class="env-val" placeholder="VALUE" value="${escapeHtml(v || '')}">
    <button type="button" class="env-del" title="remove" onclick="this.parentElement.remove()">×</button>`;
  rows.appendChild(row);
}

function renderEnvRows(envObj) {
  const rows = document.getElementById('detail-agent-env-rows');
  rows.innerHTML = '';
  Object.entries(envObj || {}).forEach(([k, v]) => addEnvRow(k, v));
}

function collectEnv() {
  const out = {};
  document.querySelectorAll('#detail-agent-env-rows .env-row').forEach(row => {
    const k = row.querySelector('.env-key').value.trim();
    if (!k) return;
    out[k] = row.querySelector('.env-val').value;
  });
  return out;
}
```

- [ ] **Step 3: app.js — wire render + both saves**

- In `renderSelectedBotAgentControls()`, after the MCP render line, add:
```js
  renderEnvRows(bot?.agent_env || {});
```
- In `saveSelectedBotAgent()`, add `agent_env: collectEnv(),` to the `api('POST', '/bots/agent', {…})` payload (after `system_prompt`); and after the response updates, add `bot.agent_env = updated.agent_env || {};`.
- In `saveSystemPrompt()`, add `agent_env: collectEnv(),` to its payload too (after `system_prompt`) so the modal save doesn't wipe env; and after success add `selectedBot().agent_env = data.data.agent_env || {};` (match how it updates the other fields).

- [ ] **Step 4: style.css — env rows**

Append:
```css
.env-rows { display: flex; flex-direction: column; gap: 6px; margin-bottom: 8px; }
.env-row { display: flex; gap: 8px; align-items: center; }
.env-row .env-key { flex: 0 0 38%; }
.env-row .env-val { flex: 1 1 auto; }
.env-row .env-del {
  flex: 0 0 auto; width: 30px; height: 30px; padding: 0; line-height: 1;
  background: var(--panel-2); border: 1px solid var(--line); border-radius: var(--r);
  color: var(--ink-faint); cursor: pointer;
}
.env-row .env-del:hover { border-color: var(--line-bright); color: var(--ink); }
.env-add { margin-top: 2px; }
```

- [ ] **Step 5: Build + serve-check**
```bash
cd /root/workspace/master/myclaw && go build ./...
go build -o /tmp/myclaw-ui . && CHANNEL_MASTER_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= CHANNEL_HTTP_ADDR=:18085 CHANNEL_SQLITE_PATH=/tmp/uitest.db /tmp/myclaw-ui & sleep 2
curl -s localhost:18085/ | grep -c detail-agent-env-rows   # expect 1
curl -s -o /dev/null -w '%{http_code}\n' localhost:18085/app.js   # 200
kill %1 2>/dev/null; rm -f /tmp/myclaw-ui /tmp/uitest.db
```
Manual: open a bot, add/remove env rows, Save, reload — rows prefill from persisted `agent_env`.

- [ ] **Step 6: Commit**
```bash
git add internal/api/http/web/static/index.html internal/api/http/web/static/app.js internal/api/http/web/static/style.css
git commit -m "feat(web): per-bot agent env key-value editor"
```

---

## Task 5: Full-suite gate

- [ ] **Step 1:** `go build ./... && go test ./...` — all pass. Fix-forward into the owning task's files if anything fails.

---

## Self-Review

**Spec coverage:** `agent_env` JSON column + migration `000010` (Task 1) ✓; model `serializer:json` + domain + repo (Task 1) ✓; db_test bump (Task 1) ✓; service `CreateBotInput`/`ConfigureBotAgentInput`/`BotListItem`/outputs + nil-normalize (Task 2) ✓; DTOs incl. `BotResponse` alias (Task 2) ✓; handlers create/configure/list (Task 2) ✓; resolver `spec.Env = bot.AgentEnv` + full `KEY=VALUE` log via `formatEnvKV` (Task 3) ✓; Web UI key-value rows editor with render/add/collect + both saves (Task 4) ✓; full-suite gate (Task 5) ✓. Out-of-scope (per-CLI env, masking, key validation, .env import) absent.

**Placeholder scan:** every code step is complete; the `serializer:json` fallback names the exact alternative (`mcp_servers.args_json` pattern). No TBD.

**Type consistency:** `AgentEnv map[string]string` (Go) / `agent_env` (column + JSON tag) used consistently across model, domain, repo, `CreateBotInput`/`CreateBotOutput`/`BotListItem`/`ConfigureBotAgentInput`, DTOs, handlers, and the JS `agent_env` key. `formatEnvKV(map[string]string) string` matches its call site. `renderEnvRows`/`addEnvRow`/`collectEnv` consistent between definition, the render call, and both saves.
