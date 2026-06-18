# Per-Bot CLI Alias Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a bot carry an optional `CLIAlias`; when set, the bot's agent CLI launches with that command (e.g. `cx` for codex), bypassing availability detection while still injecting the selected CLI's real-binary args.

**Architecture:** Add a `CLIAlias` column to `bots` (append-only migration). The resolver overrides `spec.Command` with the alias, skips the `Available` gate, and sets a new `agent.Spec.RealCLI` flag. The three ACP drivers thread that flag into `buildACPArgs` so real-binary args are injected even when the command basename isn't canonical. The alias is set via the existing per-bot agent-config endpoint and a new UI input.

**Tech Stack:** Go 1.23, GORM/SQLite, golang-migrate (embedded `*.sql`), `net/http`, vanilla JS frontend.

---

## File Structure

**Modify:**
- `internal/domain/entities.go` — `Bot.CLIAlias string`.
- `internal/store/models/bot.go` — GORM `CLIAlias` column.
- `internal/store/migrations/000005_bot_cli_alias.up.sql` — **new, append-only** migration.
- `internal/store/repositories/bot_repository.go` — map `CLIAlias` in Create, Update, `toDomainBot`.
- `internal/agent/types.go` — `Spec.RealCLI bool`.
- `internal/app/bot/cli_resolver.go` — alias override branch.
- `internal/agent/codex/driver_acp.go`, `internal/agent/claude/driver_acp.go`, `internal/agent/opencode/driver_acp.go` — `buildACPArgs` honors `RealCLI`.
- `internal/app/bot/service.go` — `ConfigureBotAgentInput.CLIAlias`, persist it, `BotListItem.CLIAlias`.
- `internal/api/http/dto/bots.go` — `ConfigureBotAgentRequest.CLIAlias`, `BotResponse.CLIAlias`.
- `internal/api/http/handlers/bots.go` — thread `cli_alias` in `ConfigureBotAgent` and out in `ListBots`.
- `internal/api/http/web/static/index.html`, `internal/api/http/web/static/app.js` — alias input + save/prefill.

**Test:** `bot_repository_test.go`, `cli_resolver_test.go`, the three `driver_acp_test.go`, `handlers/bots_test.go`.

Note: `testutil.OpenTestDB` runs `store.Migrate` (golang-migrate over the embedded `*.sql`), so once the `000005` migration exists every test DB has the `cli_alias` column.

---

## Task 1: Persist `CLIAlias` on Bot

**Files:**
- Modify: `internal/domain/entities.go` (`Bot` struct ~line 56)
- Modify: `internal/store/models/bot.go`
- Create: `internal/store/migrations/000005_bot_cli_alias.up.sql`
- Modify: `internal/store/repositories/bot_repository.go` (Create ~22, Update ~91, `toDomainBot` ~124)
- Test: `internal/store/repositories/bot_repository_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/repositories/bot_repository_test.go`:

```go
func TestBotRepositoryPreservesCLIAlias(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.Bot{
		ID:               "bot_alias_1",
		UserID:           "usr_1",
		Name:             "alias-bot",
		ChannelType:      "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
		CLIAlias:         "cx",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "bot_alias_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CLIAlias != "cx" {
		t.Fatalf("CLIAlias after create = %q, want cx", got.CLIAlias)
	}

	got.CLIAlias = ""
	if _, err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := repo.GetByID(ctx, "bot_alias_1")
	if err != nil {
		t.Fatal(err)
	}
	if again.CLIAlias != "" {
		t.Fatalf("CLIAlias after clear = %q, want empty", again.CLIAlias)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/repositories -run TestBotRepositoryPreservesCLIAlias`
Expected: COMPILE FAIL — `domain.Bot` has no field `CLIAlias`.

- [ ] **Step 3: Add the domain field**

In `internal/domain/entities.go`, in the `Bot` struct, add `CLIAlias` after `Role`:
```go
	Role              string
	CLIAlias          string
```

- [ ] **Step 4: Add the GORM model column**

In `internal/store/models/bot.go`, add after `Role`:
```go
	Role              string `gorm:"not null;default:''"`
	CLIAlias          string `gorm:"not null;default:''"`
```

- [ ] **Step 5: Add the append-only migration**

Create `internal/store/migrations/000005_bot_cli_alias.up.sql`:
```sql
ALTER TABLE bots ADD COLUMN cli_alias TEXT NOT NULL DEFAULT '';
```
(Append-only — do NOT renumber or insert before existing migrations; a re-numbered migration strands already-migrated DBs.)

- [ ] **Step 6: Map the field in the repository**

In `internal/store/repositories/bot_repository.go`, add `CLIAlias: bot.CLIAlias,` to the `models.Bot{...}` literal in **Create** (~line 31, next to `Role`), add the same line to the `models.Bot{...}` literal in **Update** (~line 100), and add `CLIAlias: m.CLIAlias,` to the `domain.Bot{...}` literal in **`toDomainBot`** (~line 135, next to `Role`).

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/store/repositories -run TestBotRepositoryPreservesCLIAlias -count=1`
Expected: PASS

- [ ] **Step 8: Run the store suite**

Run: `go test ./internal/store/... -count=1`
Expected: PASS (the new migration runs cleanly on fresh test DBs)

- [ ] **Step 9: Commit**

```bash
git add internal/domain/entities.go internal/store/models/bot.go internal/store/migrations/000005_bot_cli_alias.up.sql internal/store/repositories/bot_repository.go internal/store/repositories/bot_repository_test.go
git commit -m "feat: persist per-bot cli_alias column"
```

---

## Task 2: `agent.Spec.RealCLI` + resolver alias override

**Files:**
- Modify: `internal/agent/types.go` (`Spec` struct ~line 15)
- Modify: `internal/app/bot/cli_resolver.go` (gates ~line 70-84)
- Test: `internal/app/bot/cli_resolver_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/app/bot/cli_resolver_test.go`. This uses the existing stubs in that file — `newBotRepoStub(domain.Bot{...})` and `&agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{...}}` — and the real `Resolve(ctx, botID string)` signature (the resolver fetches the bot by ID). `errors` is already imported there.

```go
func TestResolveAliasOverridesCommandAndBypassesAvailability(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_alias", Name: "b", AgentCapabilityID: "cap_codex",
		AgentMode: "acp", CLIAlias: "cx",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		// NOT available and canonical command — alias must bypass the gate.
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"acp"}, Available: false},
	}}
	r := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

	spec, err := r.Resolve(context.Background(), "bot_alias")
	if err != nil {
		t.Fatalf("Resolve with alias should bypass availability: %v", err)
	}
	if spec.Command != "cx" {
		t.Fatalf("spec.Command = %q, want cx", spec.Command)
	}
	if !spec.RealCLI {
		t.Fatalf("spec.RealCLI = false, want true when alias set")
	}
}

func TestResolveNoAliasKeepsDefaultAndUnavailableErrors(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_noalias", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "acp",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"acp"}, Available: false},
	}}
	r := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

	if _, err := r.Resolve(context.Background(), "bot_noalias"); !errors.Is(err, ErrBotCLIUnavailable) {
		t.Fatalf("no alias + unavailable should error ErrBotCLIUnavailable, got %v", err)
	}
}
```

If the stub names differ in your copy of the file, `grep -n "func newBotRepoStub\|agentCapabilityRepoStub" internal/app/bot/cli_resolver_test.go` and match them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/bot -run TestResolveAlias -count=1`
Expected: FAIL — `spec.RealCLI` undefined, and alias path not implemented (availability still errors).

- [ ] **Step 3: Add the `RealCLI` spec field**

In `internal/agent/types.go`, in the `Spec` struct, add after `Command`/`Args` (e.g. after `Orchestrator` or near `Command`):
```go
	// RealCLI marks the command as the genuine target CLI even when its
	// basename isn't the canonical name (e.g. an operator alias like "cx"
	// for codex), so drivers still inject real-binary args.
	RealCLI bool
```

- [ ] **Step 4: Implement the alias branch in the resolver**

In `internal/app/bot/cli_resolver.go`, replace the availability/command gate and the `spec.Command` assignment. The current code is:
```go
	if !capability.Available {
		return agent.Spec{}, ErrBotCLIUnavailable
	}
	if !slices.Contains(capability.SupportedModes, bot.AgentMode) {
		return agent.Spec{}, ErrBotCLIUnsupportedMode
	}
	if capability.Command == "" {
		return agent.Spec{}, ErrBotCLIConfigMissing
	}
	spec := agent.Spec{
		BotID:      botID,
		BotName:    bot.Name,
		Type:       bot.AgentMode,
		Command:    capability.Command,
		Args:       append([]string(nil), capability.Args...),
		Timeout:    r.timeoutForMode(bot.AgentMode),
		SQLitePath: r.sqlitePath,
	}
```
Change it to (mode is always validated; availability/command checks are skipped when an alias is set; the alias overrides the command and flags `RealCLI`):
```go
	alias := strings.TrimSpace(bot.CLIAlias)
	if !slices.Contains(capability.SupportedModes, bot.AgentMode) {
		return agent.Spec{}, ErrBotCLIUnsupportedMode
	}
	if alias == "" {
		if !capability.Available {
			return agent.Spec{}, ErrBotCLIUnavailable
		}
		if capability.Command == "" {
			return agent.Spec{}, ErrBotCLIConfigMissing
		}
	}
	command := capability.Command
	if alias != "" {
		command = alias
	}
	spec := agent.Spec{
		BotID:      botID,
		BotName:    bot.Name,
		Type:       bot.AgentMode,
		Command:    command,
		Args:       append([]string(nil), capability.Args...),
		Timeout:    r.timeoutForMode(bot.AgentMode),
		SQLitePath: r.sqlitePath,
		RealCLI:    alias != "",
	}
```
Ensure `strings` is imported in `cli_resolver.go` (add to the import block if missing).

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/app/bot -run TestResolve -count=1`
Expected: PASS (both new tests and existing resolve tests)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/types.go internal/app/bot/cli_resolver.go internal/app/bot/cli_resolver_test.go
git commit -m "feat: resolver overrides command with cli alias and sets RealCLI"
```

---

## Task 3: Drivers inject real-binary args when `RealCLI` is set

**Files:**
- Modify: `internal/agent/codex/driver_acp.go` (`buildACPArgs` ~313, `Init` call site ~100)
- Modify: `internal/agent/claude/driver_acp.go` (`buildACPArgs` ~358, `Init` call site ~93)
- Modify: `internal/agent/opencode/driver_acp.go` (`buildACPArgs` ~309, `Init` call site ~101)
- Test: the three `driver_acp_test.go`

> The codex **exec** driver (`driver_exec.go`) builds `args` directly from `spec.Args` with no basename gate, so it needs no change.

- [ ] **Step 1: Write the failing tests**

In `internal/agent/codex/driver_acp_test.go` add:
```go
func TestBuildACPArgsInjectsForAliasedRealCLI(t *testing.T) {
	// basename "cx" is not canonical, but realCLI=true must still inject app-server.
	got := buildACPArgs("cx", nil, true)
	if len(got) == 0 || got[0] != "app-server" {
		t.Fatalf("expected app-server injected for realCLI alias, got %v", got)
	}
}

func TestBuildACPArgsSkipsForStubWhenNotRealCLI(t *testing.T) {
	got := buildACPArgs("/tmp/fake-codex", []string{"x"}, false)
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("expected verbatim args for stub, got %v", got)
	}
}
```
Add the equivalent in `internal/agent/claude/driver_acp_test.go` (injected marker is `-p`):
```go
func TestBuildACPArgsInjectsForAliasedRealCLI(t *testing.T) {
	got := buildACPArgs("cl", nil, true)
	if len(got) == 0 || got[0] != "-p" {
		t.Fatalf("expected -p injected for realCLI alias, got %v", got)
	}
}

func TestBuildACPArgsSkipsForStubWhenNotRealCLI(t *testing.T) {
	got := buildACPArgs("/tmp/fake-claude", []string{"x"}, false)
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("expected verbatim args for stub, got %v", got)
	}
}
```
And in `internal/agent/opencode/driver_acp_test.go` (injected marker is `acp`):
```go
func TestBuildACPArgsInjectsForAliasedRealCLI(t *testing.T) {
	got := buildACPArgs("oc", nil, true)
	if len(got) == 0 || got[0] != "acp" {
		t.Fatalf("expected acp injected for realCLI alias, got %v", got)
	}
}

func TestBuildACPArgsSkipsForStubWhenNotRealCLI(t *testing.T) {
	got := buildACPArgs("/tmp/fake-opencode", []string{"x"}, false)
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("expected verbatim args for stub, got %v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/codex ./internal/agent/claude ./internal/agent/opencode -run TestBuildACPArgs -count=1`
Expected: COMPILE FAIL — `buildACPArgs` takes 2 args, not 3.

- [ ] **Step 3: Thread `realCLI` through codex `buildACPArgs`**

In `internal/agent/codex/driver_acp.go`, change the signature and gate:
```go
func buildACPArgs(command string, args []string, realCLI bool) []string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	if base != "codex" && base != "codex.exe" && !realCLI {
		return append([]string(nil), args...)
	}
	...
```
Update the call site in `Init` (~line 100):
```go
	cmd := exec.CommandContext(ctx, spec.Command, buildACPArgs(spec.Command, spec.Args, spec.RealCLI)...)
```

- [ ] **Step 4: Thread `realCLI` through claude `buildACPArgs`**

In `internal/agent/claude/driver_acp.go`:
```go
func buildACPArgs(command string, extra []string, realCLI bool) []string {
	if !isClaudeCommand(command) && !realCLI {
		return append([]string(nil), extra...)
	}
	...
```
Update the `Init` call site (~line 93):
```go
	cmd := exec.CommandContext(ctx, spec.Command, buildACPArgs(spec.Command, spec.Args, spec.RealCLI)...)
```

- [ ] **Step 5: Thread `realCLI` through opencode `buildACPArgs`**

In `internal/agent/opencode/driver_acp.go`:
```go
func buildACPArgs(command string, args []string, realCLI bool) []string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	if base != "opencode" && base != "opencode.exe" && !realCLI {
		return append([]string(nil), args...)
	}
	...
```
Update the `Init` call site (~line 101):
```go
	cmd := exec.CommandContext(ctx, spec.Command, buildACPArgs(spec.Command, spec.Args, spec.RealCLI)...)
```

- [ ] **Step 6: Update any existing `buildACPArgs` test call sites**

In each `driver_acp_test.go`, find existing calls and add the third arg `false` (preserving current behavior):
```bash
grep -rn "buildACPArgs(" internal/agent/codex internal/agent/claude internal/agent/opencode
```
For every existing call of the old 2-arg form, append `, false`. (The new tests from Step 1 already pass the third arg.)

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/agent/... -count=1`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/agent/codex/driver_acp.go internal/agent/codex/driver_acp_test.go internal/agent/claude/driver_acp.go internal/agent/claude/driver_acp_test.go internal/agent/opencode/driver_acp.go internal/agent/opencode/driver_acp_test.go
git commit -m "feat: inject real-binary CLI args when spec.RealCLI is set"
```

---

## Task 4: API — accept and echo `cli_alias`

**Files:**
- Modify: `internal/app/bot/service.go` (`ConfigureBotAgentInput` ~289, `ConfigureBotAgent` ~295, `BotListItem` ~132)
- Modify: `internal/api/http/dto/bots.go` (`ConfigureBotAgentRequest`, `BotResponse`)
- Modify: `internal/api/http/handlers/bots.go` (`ConfigureBotAgent`, `ListBots`)
- Test: `internal/api/http/handlers/bots_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/http/handlers/bots_test.go` (uses the existing `newTestServer` + `testutil.PostJSON` harness in that file):
```go
func TestConfigureBotAgentPersistsCLIAlias(t *testing.T) {
	ts := newTestServer(t)

	create := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_1","name":"alias-bot","type":"channel","channel_type":"wechat"}`)
	if create.Code != stdhttp.StatusOK {
		t.Fatalf("create status %d: %s", create.Code, create.Body.String())
	}
	var createEnv httpapi.Envelope
	if err := json.Unmarshal(create.Body.Bytes(), &createEnv); err != nil {
		t.Fatal(err)
	}
	botID := createEnv.Data.(map[string]any)["bot_id"].(string)

	body := `{"bot_id":"` + botID + `","agent_capability_id":"cap_claude","agent_mode":"session","cli_alias":"cx"}`
	res := testutil.PostJSON(t, ts, "/api/v1/bots/agent", body)
	if res.Code != stdhttp.StatusOK {
		t.Fatalf("agent status %d: %s", res.Code, res.Body.String())
	}
	var env httpapi.Envelope
	if err := json.Unmarshal(res.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if got := env.Data.(map[string]any)["cli_alias"]; got != "cx" {
		t.Fatalf("cli_alias in response = %v, want cx", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/http/handlers -run TestConfigureBotAgentPersistsCLIAlias -count=1`
Expected: FAIL — `cli_alias` not accepted/echoed (response has no `cli_alias`).

- [ ] **Step 3: Add the field to the service input, persistence, and list item**

In `internal/app/bot/service.go`:

Add to `ConfigureBotAgentInput`:
```go
type ConfigureBotAgentInput struct {
	BotID             string
	AgentCapabilityID string
	AgentMode         string
	CLIAlias          string
}
```
In `ConfigureBotAgent`, persist it and return it. After `bot.AgentMode = input.AgentMode` add `bot.CLIAlias = strings.TrimSpace(input.CLIAlias)`, and add `CLIAlias: bot.CLIAlias,` to the returned `BotListItem{...}` literal. (Ensure `strings` is imported in service.go — it already is used elsewhere; if not, add it.)

Add to `BotListItem`:
```go
	AgentMode         string
	CLIAlias          string
```
Also map `CLIAlias: item.CLIAlias` wherever `BotListItem` is built from a bot for the **list** path (`ListBots` service method builds `BotListItem`s — add `CLIAlias: <bot>.CLIAlias` there too).

- [ ] **Step 4: Add the DTO fields**

In `internal/api/http/dto/bots.go`:
```go
type ConfigureBotAgentRequest struct {
	BotID             string `json:"bot_id"`
	AgentCapabilityID string `json:"agent_capability_id"`
	AgentMode         string `json:"agent_mode"`
	CLIAlias          string `json:"cli_alias,omitempty"`
}
```
And add to `BotResponse`:
```go
	AgentMode         string `json:"agent_mode,omitempty"`
	CLIAlias          string `json:"cli_alias,omitempty"`
```

- [ ] **Step 5: Thread it through the handlers**

In `internal/api/http/handlers/bots.go`:

In `ConfigureBotAgent`, pass the alias into the service input:
```go
		result, err := svc.ConfigureBotAgent(r.Context(), botapp.ConfigureBotAgentInput{
			BotID:             req.BotID,
			AgentCapabilityID: req.AgentCapabilityID,
			AgentMode:         req.AgentMode,
			CLIAlias:          req.CLIAlias,
		})
```
And add `CLIAlias: result.CLIAlias,` to the `dto.ConfigureBotAgentResponse{...}` (a.k.a. `BotResponse`) literal it returns.

In `ListBots`, add `CLIAlias: item.CLIAlias,` to the `dto.BotResponse{...}` literal built per bot.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/api/http/handlers -run TestConfigureBotAgentPersistsCLIAlias -count=1`
Expected: PASS

- [ ] **Step 7: Run the api suite**

Run: `go test ./internal/api/... ./internal/app/bot -count=1`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/app/bot/service.go internal/api/http/dto/bots.go internal/api/http/handlers/bots.go internal/api/http/handlers/bots_test.go
git commit -m "feat: accept and echo cli_alias on the bot agent-config endpoint"
```

---

## Task 5: Frontend — alias input in the agent card

**Files:**
- Modify: `internal/api/http/web/static/index.html` (agent card ~106-120)
- Modify: `internal/api/http/web/static/app.js` (`renderSelectedBotAgentControls` ~58, `saveSelectedBotAgent` ~63)

- [ ] **Step 1: Add the alias input to the agent card**

In `internal/api/http/web/static/index.html`, inside the `// AGENT` card's `<div class="form-grid">`, between the Mode field and the action button, insert:
```html
            <div class="form-field wide">
              <label>CLI alias <span style="opacity:.6">(optional)</span></label>
              <input id="detail-agent-alias" type="text" placeholder="e.g. cx">
            </div>
```

- [ ] **Step 2: Prefill the alias on render**

In `internal/api/http/web/static/app.js`, update `renderSelectedBotAgentControls`:
```js
function renderSelectedBotAgentControls() {
  const bot = selectedBot();
  renderCapabilityOptions('detail-agent-capability', 'detail-agent-mode', bot?.agent_capability_id || '', bot?.agent_mode || '');
  document.getElementById('detail-agent-alias').value = bot?.cli_alias || '';
}
```

- [ ] **Step 3: Send the alias on save**

In `internal/api/http/web/static/app.js`, update `saveSelectedBotAgent` to read the input, include it in the POST, and write it back:
```js
async function saveSelectedBotAgent() {
  const bot = selectedBot();
  if (!bot) { toast('select a bot'); return; }
  const agentCapabilityID = document.getElementById('detail-agent-capability').value;
  const agentMode = document.getElementById('detail-agent-mode').value;
  const cliAlias = document.getElementById('detail-agent-alias').value.trim();
  if (!agentCapabilityID || !agentMode) { toast('capability and mode required'); return; }
  const data = await api('POST', '/bots/agent', {
    bot_id: bot.bot_id,
    agent_capability_id: agentCapabilityID,
    agent_mode: agentMode,
    cli_alias: cliAlias,
  });
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  const updated = data.data;
  bot.agent_capability_id = updated.agent_capability_id;
  bot.agent_mode = updated.agent_mode;
  bot.cli_alias = updated.cli_alias || '';
  renderSelectedBotAgentControls();
  renderBotList();
  renderDetail();
  toast('agent updated');
}
```

- [ ] **Step 4: Verify static assets build**

Run: `go build ./... && go test ./internal/api/http/web -count=1`
Expected: PASS (embed test still finds the static files)

- [ ] **Step 5: Commit**

```bash
git add internal/api/http/web/static/index.html internal/api/http/web/static/app.js
git commit -m "feat: add CLI alias input to the bot agent-config UI"
```

---

## Task 6: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | grep -vE '^ok|no test files'`
Expected: clean build/vet; no `FAIL` lines.

- [ ] **Step 2: Manual smoke (optional)**

```bash
export CHANNEL_MASTER_KEY=$(openssl rand -base64 32)
go run ./cmd/server
```
In the UI: open a bot, in the AGENT card set a capability + mode + a CLI alias (e.g. `cx`), Save, reload, confirm the alias persists. (Launching the aliased CLI end-to-end requires the alias binary to exist on PATH.)

- [ ] **Step 3: Final commit (if any fixes were needed)**

```bash
git add -A
git commit -m "chore: verification fixes for per-bot cli alias"
```

---

## Self-Review Notes

- **Spec coverage:** data model + migration (Task 1), `RealCLI` flag + resolver bypass/override (Task 2), driver arg-injection under alias (Task 3), API accept/echo (Task 4), UI (Task 5), verification (Task 6). All spec sections map to a task.
- **Migration hazard:** Task 1 Step 5 explicitly appends `000005` and warns against renumbering — directly addresses the spec's flagged risk.
- **Type consistency:** `CLIAlias` (domain/model/BotListItem/Input), `cli_alias` (json + SQL column), and `spec.RealCLI` are used identically across Tasks 1–5. `buildACPArgs(command, args, realCLI)` signature is consistent across all three drivers and their call sites.
- **Capability-dropdown risk (from spec):** the resolver now bypasses `Available` when an alias is set, so a bot pointed at an undetected CLI still launches. The UI capability dropdown is populated from `ListAgentCapabilities`; if it ever hides unavailable capabilities, selecting one for aliasing could be blocked — out of scope for this plan (noted), since the dropdown currently lists capabilities returned by the service regardless of a per-bot alias.
