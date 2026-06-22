# Per-bot System Prompt Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each bot a DB-stored system prompt that the resolver materializes into the bot's workspace as the CLI's native instruction file (`AGENTS.md` for codex/opencode, `CLAUDE.md` for claude) at launch.

**Architecture:** Add a `system_prompt` column end-to-end (migration → GORM model → domain entity → repo mapping → service inputs/outputs → DTO → handlers → Web UI). In `BotCLIResolver.Resolve`, after the workspace `MkdirAll`, write the prompt to the CLI's doc file (non-empty ⇒ overwrite, empty ⇒ delete). myclaw fully owns that file.

**Tech Stack:** Go 1.23, GORM + SQLite (golang-migrate up-only migrations, embedded), `net/http`, vanilla JS/HTML static UI.

**Spec:** `docs/superpowers/specs/2026-06-22-per-bot-system-prompt-design.md`.

**Deploy / worktree strategy (IMPORTANT):** The live service is `air`-watched on the **main checkout** — any half-built `.go` file saved there auto-deploys. So implement this whole feature in **one isolated git worktree off `main`**, on branch `feat/per-bot-system-prompt`, and **merge once** at the end (atomic deploy). Do NOT edit the main checkout's Go files mid-build. Task 6 (rollout) stages `shiben.system_prompt` in the live DB **before** that merge so the new resolver rewrites (never deletes) shiben's existing routing file.

---

## Task 1: DB column + model + domain + repo mapping

**Files:**
- Create: `internal/store/migrations/000009_bot_system_prompt.up.sql`
- Modify: `internal/store/models/bot.go`, `internal/domain/entities.go`, `internal/store/repositories/bot_repository.go`
- Test: `internal/store/repositories/bot_repository_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bot_repository_test.go`:
```go
func TestBotRepositorySystemPromptRoundTrip(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_sp",
		UserID:           "usr_1",
		Name:             "router",
		ChannelType:      "feishu",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
		SystemPrompt:     "you are a router",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SystemPrompt != "you are a router" {
		t.Fatalf("create system_prompt = %q", created.SystemPrompt)
	}

	got, err := repo.GetByID(ctx, "bot_sp")
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "you are a router" {
		t.Fatalf("get system_prompt = %q", got.SystemPrompt)
	}

	got.SystemPrompt = ""
	updated, err := repo.Update(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SystemPrompt != "" {
		t.Fatalf("after clear system_prompt = %q", updated.SystemPrompt)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`domain.Bot` has no `SystemPrompt`)

Run: `go test ./internal/store/repositories/ -run TestBotRepositorySystemPromptRoundTrip`
Expected: compile error `unknown field SystemPrompt`.

- [ ] **Step 3: Add the migration**

`internal/store/migrations/000009_bot_system_prompt.up.sql` (this project's migrations are **up-only** — no `.down.sql`):
```sql
ALTER TABLE bots ADD COLUMN system_prompt TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 4: Add the field to model + domain + repo mapping**

`internal/store/models/bot.go` — add after the `Workspace` field:
```go
	SystemPrompt      string `gorm:"not null;default:'';column:system_prompt"`
```
`internal/domain/entities.go` — in `type Bot struct`, add after `Workspace string`:
```go
	SystemPrompt      string
```
`internal/store/repositories/bot_repository.go` — add `SystemPrompt: bot.SystemPrompt,` to the `models.Bot{…}` literal in **both** `Create` (after `Workspace`) and `Update` (after `Workspace`), and add `SystemPrompt: m.SystemPrompt,` to `toDomainBot` (after `Workspace`).

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./internal/store/repositories/ -run TestBotRepositorySystemPromptRoundTrip -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/000009_bot_system_prompt.up.sql internal/store/models/bot.go internal/domain/entities.go internal/store/repositories/bot_repository.go internal/store/repositories/bot_repository_test.go
git commit -m "feat(bot): add system_prompt column (migration, model, domain, repo)"
```
(Trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.)

---

## Task 2: Service + DTO + handlers plumbing

**Files:**
- Modify: `internal/app/bot/service.go`, `internal/api/http/dto/bots.go`, `internal/api/http/handlers/bots.go`
- Test: `internal/app/bot/service_test.go`

- [ ] **Step 1: Write the failing test**

Append to `service_test.go`:
```go
func TestBotServiceConfigureBotAgentSystemPrompt(t *testing.T) {
	svc := newTestBotService(t)
	created, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_sp",
		Name:           "router",
		ChannelType:    "feishu",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.ConfigureBotAgent(context.Background(), ConfigureBotAgentInput{
		BotID:             created.BotID,
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
		SystemPrompt:      "  you are a router  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.SystemPrompt != "you are a router" {
		t.Fatalf("returned system_prompt = %q (want trimmed)", updated.SystemPrompt)
	}

	stored, err := svc.bots.GetByID(context.Background(), created.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SystemPrompt != "you are a router" {
		t.Fatalf("stored system_prompt = %q", stored.SystemPrompt)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`ConfigureBotAgentInput` / `BotListItem` have no `SystemPrompt`)

Run: `go test ./internal/app/bot/ -run TestBotServiceConfigureBotAgentSystemPrompt`
Expected: compile error `unknown field SystemPrompt`.

- [ ] **Step 3: Add `SystemPrompt` to service inputs/outputs and persist it**

In `internal/app/bot/service.go`:
- `CreateBotInput`: add `SystemPrompt string` (after `AgentMode`).
- `CreateBotOutput`: add `SystemPrompt string` (after `AgentMode`).
- `CreateBot`: in the `domain.Bot{…}` passed to `s.bots.Create`, add `SystemPrompt: input.SystemPrompt,`; in the returned `CreateBotOutput{…}`, add `SystemPrompt: bot.SystemPrompt,`.
- `BotListItem`: add `SystemPrompt string` (after `AgentMode`).
- `ListBots`: in the `BotListItem{…}` literal it appends, add `SystemPrompt: bot.SystemPrompt,`.
- `ConfigureBotAgentInput`: add `SystemPrompt string` (after `AgentMode`).
- `ConfigureBotAgent`: after `bot.CLIAlias = strings.TrimSpace(input.CLIAlias)`, add `bot.SystemPrompt = strings.TrimSpace(input.SystemPrompt)`; in the returned `BotListItem{…}`, add `SystemPrompt: bot.SystemPrompt,`.

- [ ] **Step 4: Add `system_prompt` to the DTOs**

In `internal/api/http/dto/bots.go` add `SystemPrompt string `json:"system_prompt,omitempty"`` to:
- `CreateBotRequest` (after `AgentMode`)
- `CreateBotResponse` (after `AgentMode`)
- `BotResponse` (after `AgentMode`) — note `ConfigureBotAgentResponse = BotResponse` (alias), so this covers the configure response and the list response.
- `ConfigureBotAgentRequest` (after `AgentMode`)

- [ ] **Step 5: Plumb the field through the handlers**

In `internal/api/http/handlers/bots.go`:
- `CreateBot`: add `SystemPrompt: req.SystemPrompt,` to `botapp.CreateBotInput{…}`, and `SystemPrompt: result.SystemPrompt,` to `dto.CreateBotResponse{…}`.
- `ListBots`: add `SystemPrompt: item.SystemPrompt,` to the `dto.BotResponse{…}` literal.
- `ConfigureBotAgent`: add `SystemPrompt: req.SystemPrompt,` to `botapp.ConfigureBotAgentInput{…}`, and `SystemPrompt: result.SystemPrompt,` to `dto.ConfigureBotAgentResponse{…}`.

- [ ] **Step 6: Run — expect PASS** (+ no regressions)

Run: `go test ./internal/app/bot/ -run TestBotServiceConfigureBotAgentSystemPrompt -v && go build ./...`
Expected: PASS, build clean.

- [ ] **Step 7: Commit**

```bash
git add internal/app/bot/service.go internal/app/bot/service_test.go internal/api/http/dto/bots.go internal/api/http/handlers/bots.go
git commit -m "feat(bot): carry system_prompt through service, DTOs, handlers"
```

---

## Task 3: Resolver writes/deletes the workspace doc file

**Files:**
- Modify: `internal/app/bot/cli_resolver.go`
- Test: `internal/app/bot/cli_resolver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cli_resolver_test.go` (imports `os`, `path/filepath`, `time`, `domain` already present):
```go
func TestResolveWritesSystemPromptToAgentsForCodex(t *testing.T) {
	ws := t.TempDir()
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_sp",
		Name:              "router",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
		Workspace:         ws,
		SystemPrompt:      "route everything",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "/usr/local/bin/codex", SupportedModes: []string{"codex-exec"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{Timeout: time.Minute})

	if _, err := r.Resolve(context.Background(), "bot_sp"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md not written: %v", err)
	}
	if string(got) != "route everything" {
		t.Fatalf("AGENTS.md content = %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(ws, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("CLAUDE.md must not exist for codex")
	}
}

func TestResolveWritesSystemPromptToClaudeMdForClaude(t *testing.T) {
	ws := t.TempDir()
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_sp",
		Name:              "router",
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
		Workspace:         ws,
		SystemPrompt:      "claude router",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_claude": {ID: "cap_claude", Key: "claude", Command: "/usr/local/bin/claude", SupportedModes: []string{"session"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{Timeout: time.Minute})

	if _, err := r.Resolve(context.Background(), "bot_sp"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ws, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md not written: %v", err)
	}
	if string(got) != "claude router" {
		t.Fatalf("CLAUDE.md content = %q", string(got))
	}
}

func TestResolveEmptySystemPromptRemovesDocFile(t *testing.T) {
	ws := t.TempDir()
	stale := filepath.Join(ws, "AGENTS.md")
	if err := os.WriteFile(stale, []byte("old prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_sp",
		Name:              "router",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
		Workspace:         ws,
		SystemPrompt:      "",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "/usr/local/bin/codex", SupportedModes: []string{"codex-exec"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{Timeout: time.Minute})

	if _, err := r.Resolve(context.Background(), "bot_sp"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("empty prompt should have removed AGENTS.md")
	}
}

func TestResolveOverwritesExistingDocFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_sp",
		Name:              "router",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
		Workspace:         ws,
		SystemPrompt:      "new prompt",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "/usr/local/bin/codex", SupportedModes: []string{"codex-exec"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{Timeout: time.Minute})

	if _, err := r.Resolve(context.Background(), "bot_sp"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if string(got) != "new prompt" {
		t.Fatalf("AGENTS.md content = %q", string(got))
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (no file written/removed yet)

Run: `go test ./internal/app/bot/ -run 'TestResolve(Writes|Empty|Overwrites)' -v`
Expected: FAIL (AGENTS.md/CLAUDE.md not written; stale file not removed).

- [ ] **Step 3: Implement the write in `cli_resolver.go`**

In `Resolve`, immediately **after** the `if workDir != "" { spec.WorkDir = workDir; os.MkdirAll(...) }` block and before the `r.sessions` block, add:
```go
	if spec.WorkDir != "" {
		if err := writeSystemPromptDoc(spec.WorkDir, capability.Key, bot.SystemPrompt); err != nil {
			return agent.Spec{}, err
		}
	}
```
Add this package-level function (e.g. just below `Resolve`):
```go
// writeSystemPromptDoc materializes the bot's system prompt into the CLI's native
// instruction file in the workspace (claude → CLAUDE.md, else → AGENTS.md). myclaw
// fully owns the file: a non-empty prompt overwrites it; an empty prompt removes it.
func writeSystemPromptDoc(workDir, cliKey, prompt string) error {
	docFile := "AGENTS.md"
	if cliKey == "claude" {
		docFile = "CLAUDE.md"
	}
	path := filepath.Join(workDir, docFile)
	if strings.TrimSpace(prompt) != "" {
		return os.WriteFile(path, []byte(prompt), 0o644)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
```
(`os`, `errors`, `strings`, `path/filepath` are already imported in `cli_resolver.go`.)

- [ ] **Step 4: Run — expect PASS** (+ whole bot package)

Run: `go test ./internal/app/bot/ -v`
Expected: all PASS (4 new + existing resolver/service tests).

- [ ] **Step 5: Commit**

```bash
git add internal/app/bot/cli_resolver.go internal/app/bot/cli_resolver_test.go
git commit -m "feat(bot): resolver writes system_prompt to workspace AGENTS.md/CLAUDE.md"
```

---

## Task 4: Web UI — system prompt textarea

**Files:**
- Modify: `internal/api/http/web/static/index.html`, `internal/api/http/web/static/app.js`
- Test: manual + API (no JS test harness in this repo).

- [ ] **Step 1: Add the textarea to the agent-config form**

In `internal/api/http/web/static/index.html`, inside the agent-config `form-grid`, **between** the MCP-servers field (`<div class="form-field wide"> … #detail-agent-mcp …</div>`, ends ~line 137) and the `form-field action` Save button, insert:
```html
              <div class="form-field wide">
                <label for="detail-agent-system-prompt">System prompt <span style="opacity:.6">(optional)</span></label>
                <textarea id="detail-agent-system-prompt" rows="6" placeholder="Written to AGENTS.md (codex/opencode) or CLAUDE.md (claude) in the bot workspace on launch."></textarea>
              </div>
```

- [ ] **Step 2: Prefill on render**

In `internal/api/http/web/static/app.js`, in `renderSelectedBotAgentControls()`, after the `detail-agent-alias` line, add:
```js
  document.getElementById('detail-agent-system-prompt').value = bot?.system_prompt || '';
```

- [ ] **Step 3: Send + update on save**

In `saveSelectedBotAgent()`:
- after `const cliAlias = …` add:
```js
  const systemPrompt = document.getElementById('detail-agent-system-prompt').value;
```
- add `system_prompt: systemPrompt,` to the `api('POST', '/bots/agent', {…})` payload object.
- after `bot.cli_alias = updated.cli_alias || '';` add:
```js
  bot.system_prompt = updated.system_prompt || '';
```

- [ ] **Step 4: Build + manual verify**

```bash
go build ./...                                  # embedded static files compile
grep -n "detail-agent-system-prompt" internal/api/http/web/static/index.html internal/api/http/web/static/app.js
```
Expected: build clean; grep shows the id in both files (markup + 3 JS references). Manual: load the UI, open a bot, set a prompt, Save, reload — textarea prefills from the persisted value.

- [ ] **Step 5: Commit**

```bash
git add internal/api/http/web/static/index.html internal/api/http/web/static/app.js
git commit -m "feat(web): system prompt textarea in bot agent config"
```

---

## Task 5: Full-suite gate

**Files:** none (verification).

- [ ] **Step 1: Run the whole suite + build**

```bash
go build ./... && go test ./...
```
Expected: all packages PASS. Fix-forward into the owning task's files if anything fails.

- [ ] **Step 2: Commit** (only if a fix was needed; otherwise skip)

---

## Task 6: Rollout — migrate shiben's routing prompt into the DB (operational)

**Files:** none (live DB + deploy). Run **after** Tasks 1–5 are committed on `feat/per-bot-system-prompt`, and **before/at** merge.

- [ ] **Step 1: Stage shiben's prompt in the live DB *before* merging**

The new resolver, once deployed, treats an empty `system_prompt` as "delete the doc file" — which would delete shiben's hand-written `AGENTS.md`. Set the DB value first (old running code ignores the column, so this is a safe no-op until deploy):
```bash
DB=/root/.myclaw/myclaw.db
cp "$DB" "$DB.bak.predeploy"
# Load the current hand-written routing prompt as the DB value:
PROMPT=$(cat /root/.myclaw/bots/bot_01kvpjz52y9fethqktn07s6q56/workspace/AGENTS.md)
sqlite3 "$DB" "UPDATE bots SET system_prompt = :p WHERE id = 'bot_01kvpjz52y9fethqktn07s6q56';" \
  -- ".param set :p \"$PROMPT\""   # or use a small here-doc; verify with the SELECT below
sqlite3 "$DB" "SELECT length(system_prompt) FROM bots WHERE id='bot_01kvpjz52y9fethqktn07s6q56';"
```
Expected: non-zero length matching the file. (If `.param` quoting is awkward, write the prompt via the **configure API** instead — `POST /api/v1/bots/agent` with the bot's existing capability/mode/mcp_server_ids **plus** `system_prompt` — so MCP attachments are preserved.)

- [ ] **Step 2: Merge the feature branch → air deploys**

```bash
cd /root/workspace/master/myclaw
git merge --no-ff feat/per-bot-system-prompt
```
air rebuilds; confirm a fresh `tmp/myclaw` mtime + `/healthz` 200 (per the established redeploy check).

- [ ] **Step 3: Verify shiben's file is now DB-managed**

Clear shiben's stored codex session (forces a fresh session) and confirm the next resolve rewrites the file identically:
```bash
DB=/root/.myclaw/myclaw.db
sqlite3 "$DB" "DELETE FROM bot_cli_sessions WHERE bot_id='bot_01kvpjz52y9fethqktn07s6q56';"
# Send shiben a feishu message (or trigger a resolve); then:
diff <(sqlite3 "$DB" "SELECT system_prompt FROM bots WHERE id='bot_01kvpjz52y9fethqktn07s6q56';") \
     /root/.myclaw/bots/bot_01kvpjz52y9fethqktn07s6q56/workspace/AGENTS.md && echo "file matches DB"
```
Expected: AGENTS.md content equals the DB `system_prompt` (file rewritten from DB, not deleted). Routing still works.

---

## Self-Review

**Spec coverage:** `system_prompt` column + migration `000009` (Task 1) ✓; model/domain/repo mapping (Task 1) ✓; service `CreateBotInput`/`ConfigureBotAgentInput`/`BotListItem`/outputs (Task 2) ✓; DTOs incl. `BotResponse` alias for configure response (Task 2) ✓; handlers create/configure/list (Task 2) ✓; resolver claude→`CLAUDE.md` else→`AGENTS.md`, non-empty overwrite / empty delete / fatal-on-error (Task 3) ✓; Web UI textarea with prefill + save (Task 4) ✓; full-suite gate (Task 5) ✓; rollout migrating shiben + atomic-merge ordering (Task 6) ✓. Out-of-scope items (per-CLI variants, templating, orchestrator-merge, managed-marker, versioning) absent.

**Placeholder scan:** every code step shows complete code; the one operational quoting caveat in Task 6 Step 1 gives a concrete fallback (configure API). No "TBD".

**Type consistency:** `SystemPrompt string` (Go field) and `system_prompt` (DB column / JSON tag) used consistently across model, domain, repo, `CreateBotInput`/`CreateBotOutput`/`BotListItem`/`ConfigureBotAgentInput`, DTOs, handlers, and the JS `system_prompt` key. `writeSystemPromptDoc(workDir, cliKey, prompt string) error` signature matches its single call site. `capability.Key` is the CLI discriminator used in both the resolver code and Task 3 tests.
