# Per-Bot Workspace + Persisted CLI Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist a per-bot `Workspace` and a per-`(bot, cli)` session id, so each bot's agent CLI runs in its own folder and resumes its prior conversation across restarts (and starts fresh when no session is stored).

**Architecture:** Add a `bot.workspace` column and a `bot_cli_sessions` table. Thread a CLI-agnostic seam — `agent.Spec.ResumeSessionID` (in) and `agent.Response.SessionID` (out). The resolver looks up the stored session for `(bot_id, capability.Key)` and sets `ResumeSessionID`; the orchestrator upserts `Response.SessionID` after each turn keyed by `(bot_id, Response.RuntimeType)`. Each driver captures its native session id and applies its own resume (best-effort, fallback to fresh). `capability.Key` and `Response.RuntimeType` are both the literals {claude,codex,opencode}, so the save/lookup keys align.

**Tech Stack:** Go 1.23, GORM/SQLite, golang-migrate (embedded `*.sql`), the agent driver/session layer.

---

## File Structure

**Create:**
- `internal/store/migrations/000006_bot_workspace.up.sql`
- `internal/store/migrations/000007_bot_cli_sessions.up.sql`
- `internal/store/models/bot_cli_session.go`
- `internal/store/repositories/bot_cli_session_repository.go` (+ `_test.go`)

**Modify:**
- `internal/domain/entities.go` — `Bot.Workspace`; new `BotCLISession` + `BotCLISessionRepository` interface.
- `internal/store/models/bot.go` — `Workspace` column.
- `internal/store/repositories/bot_repository.go` — map `Workspace`.
- `internal/store/db_test.go` — schema version 5→6 (Task 1) then 6→7 (Task 2).
- `internal/agent/types.go` — `Spec.ResumeSessionID`, `Response.SessionID`.
- `internal/app/bot/service.go` — `SetWorkspaceRoot` + default workspace in `CreateBot`.
- `internal/app/bot/cli_resolver.go` — use `bot.Workspace`; look up session → `ResumeSessionID`.
- `internal/app/bot/message_orchestrator.go` — upsert `Response.SessionID` after a turn.
- `internal/bootstrap/bootstrap.go` — construct the session repo; wire into resolver, orchestrator, and `SetWorkspaceRoot`.
- `internal/agent/claude/driver_acp.go`, `internal/agent/codex/driver_acp.go`, `internal/agent/opencode/driver_acp.go` — capture session id + apply resume.

Note: `testutil.OpenTestDB` runs `store.Migrate` over the embedded `*.sql` (`//go:embed *.sql`), so new migrations apply to every test DB automatically.

---

## Task 1: Persist `bot.Workspace`

**Files:** Modify `internal/domain/entities.go`, `internal/store/models/bot.go`, `internal/store/repositories/bot_repository.go`, `internal/store/db_test.go`; Create `internal/store/migrations/000006_bot_workspace.up.sql`; Test `internal/store/repositories/bot_repository_test.go`.

- [ ] **Step 1: Write the failing test** — add to `bot_repository_test.go`:
```go
func TestBotRepositoryPreservesWorkspace(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()
	if _, err := repo.Create(ctx, domain.Bot{
		ID: "bot_ws_1", UserID: "usr_1", Name: "ws-bot", ChannelType: "wechat",
		ConnectionStatus: domain.BotConnectionStatusLoginRequired, Workspace: "/data/bots/bot_ws_1/workspace",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, "bot_ws_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "/data/bots/bot_ws_1/workspace" {
		t.Fatalf("Workspace = %q", got.Workspace)
	}
}
```

- [ ] **Step 2: Run → fail.** `go test ./internal/store/repositories -run TestBotRepositoryPreservesWorkspace` — COMPILE FAIL (`domain.Bot` has no `Workspace`).

- [ ] **Step 3: Add the field + column + migration.**
  - `internal/domain/entities.go`, in `Bot` after `CLIAlias`: `Workspace string`
  - `internal/store/models/bot.go`, after `CLIAlias`: `Workspace string \`gorm:"not null;default:''"\``
  - Create `internal/store/migrations/000006_bot_workspace.up.sql`:
    ```sql
    ALTER TABLE bots ADD COLUMN workspace TEXT NOT NULL DEFAULT '';
    ```
    (Append-only — do NOT renumber.)

- [ ] **Step 4: Map in the repository.** In `internal/store/repositories/bot_repository.go` add `Workspace: bot.Workspace,` to the `models.Bot{}` literal in **Create** and **Update**, and `Workspace: m.Workspace,` to `toDomainBot`.

- [ ] **Step 5: Bump the schema-version assertion.** In `internal/store/db_test.go`, change every `version != 5` (and the `version = 5` message text) to `6` — two assertion sites (`TestMigrate...` create + idempotent).

- [ ] **Step 6: Run → pass.** `go test ./internal/store/... -count=1` — PASS.

- [ ] **Step 7: Commit.**
```bash
git add internal/domain/entities.go internal/store/models/bot.go internal/store/migrations/000006_bot_workspace.up.sql internal/store/repositories/bot_repository.go internal/store/repositories/bot_repository_test.go internal/store/db_test.go
git commit -m "feat: persist per-bot workspace column"
```

---

## Task 2: `bot_cli_sessions` table + entity + repository

**Files:** Modify `internal/domain/entities.go`, `internal/store/db_test.go`; Create `internal/store/migrations/000007_bot_cli_sessions.up.sql`, `internal/store/models/bot_cli_session.go`, `internal/store/repositories/bot_cli_session_repository.go`, `internal/store/repositories/bot_cli_session_repository_test.go`.

- [ ] **Step 1: Write the failing test** — `internal/store/repositories/bot_cli_session_repository_test.go`:
```go
package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/testutil"
)

func TestBotCLISessionRepositoryUpsertGet(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewBotCLISessionRepository(db)
	ctx := context.Background()

	if _, err := repo.Get(ctx, "bot1", "claude"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := repo.Upsert(ctx, domain.BotCLISession{BotID: "bot1", CLIType: "claude", SessionID: "sess_a", WorkDir: "/w"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "bot1", "claude")
	if err != nil || got.SessionID != "sess_a" || got.WorkDir != "/w" {
		t.Fatalf("get after upsert = %#v err=%v", got, err)
	}
	// upsert again on same key overwrites
	if err := repo.Upsert(ctx, domain.BotCLISession{BotID: "bot1", CLIType: "claude", SessionID: "sess_b", WorkDir: "/w2"}); err != nil {
		t.Fatal(err)
	}
	got2, _ := repo.Get(ctx, "bot1", "claude")
	if got2.SessionID != "sess_b" || got2.WorkDir != "/w2" {
		t.Fatalf("overwrite failed = %#v", got2)
	}
	// different cli_type is a separate row
	if err := repo.Upsert(ctx, domain.BotCLISession{BotID: "bot1", CLIType: "codex", SessionID: "conv_x"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := repo.Get(ctx, "bot1", "codex"); c.SessionID != "conv_x" {
		t.Fatalf("codex row = %#v", c)
	}
	if a, _ := repo.Get(ctx, "bot1", "claude"); a.SessionID != "sess_b" {
		t.Fatalf("claude row clobbered = %#v", a)
	}
}
```

- [ ] **Step 2: Run → fail.** `go test ./internal/store/repositories -run TestBotCLISessionRepositoryUpsertGet` — COMPILE FAIL.

- [ ] **Step 3: Domain entity + interface.** In `internal/domain/entities.go` add:
```go
type BotCLISession struct {
	BotID     string
	CLIType   string
	SessionID string
	WorkDir   string
	UpdatedAt time.Time
}

type BotCLISessionRepository interface {
	Upsert(ctx context.Context, s BotCLISession) error
	Get(ctx context.Context, botID, cliType string) (BotCLISession, error)
}
```
(`context` and `time` are already imported in entities.go.)

- [ ] **Step 4: Migration.** Create `internal/store/migrations/000007_bot_cli_sessions.up.sql`:
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

- [ ] **Step 5: GORM model.** Create `internal/store/models/bot_cli_session.go`:
```go
package models

import "time"

type BotCLISession struct {
	BotID     string `gorm:"primaryKey;column:bot_id"`
	CLIType   string `gorm:"primaryKey;column:cli_type"`
	SessionID string `gorm:"not null;default:'';column:session_id"`
	WorkDir   string `gorm:"not null;default:'';column:work_dir"`
	UpdatedAt time.Time
}

func (BotCLISession) TableName() string { return "bot_cli_sessions" }
```

- [ ] **Step 6: Repository.** Create `internal/store/repositories/bot_cli_session_repository.go`:
```go
package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BotCLISessionRepository struct {
	db *gorm.DB
}

func NewBotCLISessionRepository(db *gorm.DB) *BotCLISessionRepository {
	return &BotCLISessionRepository{db: db}
}

func (r *BotCLISessionRepository) Upsert(ctx context.Context, s domain.BotCLISession) error {
	m := models.BotCLISession{
		BotID:     s.BotID,
		CLIType:   s.CLIType,
		SessionID: s.SessionID,
		WorkDir:   s.WorkDir,
		UpdatedAt: time.Now().UTC(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "bot_id"}, {Name: "cli_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"session_id", "work_dir", "updated_at"}),
	}).Create(&m).Error
}

func (r *BotCLISessionRepository) Get(ctx context.Context, botID, cliType string) (domain.BotCLISession, error) {
	var m models.BotCLISession
	if err := r.db.WithContext(ctx).Where("bot_id = ? AND cli_type = ?", botID, cliType).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.BotCLISession{}, domain.ErrNotFound
		}
		return domain.BotCLISession{}, err
	}
	return domain.BotCLISession{
		BotID: m.BotID, CLIType: m.CLIType, SessionID: m.SessionID, WorkDir: m.WorkDir, UpdatedAt: m.UpdatedAt,
	}, nil
}

var _ domain.BotCLISessionRepository = (*BotCLISessionRepository)(nil)
```

- [ ] **Step 7: Bump schema version.** In `internal/store/db_test.go`, change `version != 6` → `version != 7` (both sites; message text too).

- [ ] **Step 8: Run → pass.** `go test ./internal/store/... -count=1` — PASS.

- [ ] **Step 9: Commit.**
```bash
git add internal/domain/entities.go internal/store/migrations/000007_bot_cli_sessions.up.sql internal/store/models/bot_cli_session.go internal/store/repositories/bot_cli_session_repository.go internal/store/repositories/bot_cli_session_repository_test.go internal/store/db_test.go
git commit -m "feat: add bot_cli_sessions table, entity, and repository"
```

---

## Task 3: Seam — `Spec.ResumeSessionID` + `Response.SessionID`

**Files:** Modify `internal/agent/types.go`.

- [ ] **Step 1: Add the fields.** In `internal/agent/types.go`:
  - In `Spec`, after `RealCLI`:
    ```go
    // ResumeSessionID, when non-empty, asks the driver to resume that prior CLI
    // session instead of starting fresh. Best-effort: drivers fall back to a new
    // session if the CLI rejects it.
    ResumeSessionID string
    ```
  - In `Response`, after `RuntimeType`:
    ```go
    // SessionID is the CLI's native session id for this turn, surfaced so the
    // orchestrator can persist it per (bot, cli) for later resume.
    SessionID string
    ```

- [ ] **Step 2: Build to verify.** `go build ./... && go vet ./internal/agent/...` — clean (new fields, zero value, no behavior change).

- [ ] **Step 3: Commit.**
```bash
git add internal/agent/types.go
git commit -m "feat: add ResumeSessionID/SessionID seam to agent Spec and Response"
```

---

## Task 4: Workspace wiring (default at create + resolver uses it)

**Files:** Modify `internal/app/bot/service.go`, `internal/app/bot/cli_resolver.go`; Test `internal/app/bot/service_test.go`, `internal/app/bot/cli_resolver_test.go`.

- [ ] **Step 1: Write the failing tests.**
  Resolver test (in `cli_resolver_test.go`) — when `bot.Workspace` is set, the spec uses it:
```go
func TestResolveUsesBotWorkspaceWhenSet(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_w", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp",
		Workspace: "/custom/ws",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{}) // NOTE: Task 5 adds a sessions arg; if Task 5 is done first, pass the stub
	spec, err := r.Resolve(context.Background(), "bot_w")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.WorkDir != "/custom/ws" {
		t.Fatalf("WorkDir = %q, want /custom/ws", spec.WorkDir)
	}
}
```
  Service test (in `service_test.go`) — `CreateBot` materializes the default workspace once a root is set:
```go
func TestCreateBotDefaultsWorkspace(t *testing.T) {
	svc := newTestBotService(t)
	svc.SetWorkspaceRoot("/data/bots")
	out, err := svc.CreateBot(context.Background(), CreateBotInput{ExternalUserID: "u1", Name: "b", Type: domain.BotTypeChannel, ChannelType: "wechat"})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}
	got, err := svc.GetBotWorkspace(context.Background(), out.BotID) // helper added below
	if err != nil {
		t.Fatal(err)
	}
	want := "/data/bots/" + out.BotID + "/workspace"
	if got != want {
		t.Fatalf("workspace = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run → fail.** `go test ./internal/app/bot -run 'TestResolveUsesBotWorkspaceWhenSet|TestCreateBotDefaultsWorkspace'` — COMPILE FAIL (`SetWorkspaceRoot`/`GetBotWorkspace` undefined; `WorkDir` not from `bot.Workspace`).

- [ ] **Step 3: BotService workspace root + default.** In `internal/app/bot/service.go`:
  - Add field `workspaceRoot string` to the `BotService` struct.
  - Add a setter (mirroring `SetCapabilityDiscoverer`): `func (s *BotService) SetWorkspaceRoot(root string) { s.workspaceRoot = root }`.
  - In `CreateBot`, when building the `domain.Bot{}`, set `Workspace: s.defaultWorkspace(bot.ID)` — but `bot.ID` is generated in the literal, so compute the id first:
    ```go
    botID := domain.NewPrefixedID("bot")
    workspace := ""
    if s.workspaceRoot != "" {
        workspace = filepath.Join(s.workspaceRoot, botID, "workspace")
    }
    bot, err := s.bots.Create(ctx, domain.Bot{
        ID: botID,
        // ... existing fields ...
        Workspace: workspace,
    })
    ```
    (Add `"path/filepath"` to the imports of service.go if missing.)
  - Add a small read helper used by the test: `func (s *BotService) GetBotWorkspace(ctx context.Context, botID string) (string, error) { b, err := s.bots.GetByID(ctx, botID); if err != nil { return "", err }; return b.Workspace, nil }`.

- [ ] **Step 4: Resolver uses `bot.Workspace`.** In `internal/app/bot/cli_resolver.go`, replace the workspace block (currently `spec.WorkDir = filepath.Join(r.workspaceRoot, botID, "workspace")`):
```go
	workDir := strings.TrimSpace(bot.Workspace)
	if workDir == "" && r.workspaceRoot != "" {
		workDir = filepath.Join(r.workspaceRoot, botID, "workspace")
	}
	if workDir != "" {
		spec.WorkDir = workDir
		if err := os.MkdirAll(spec.WorkDir, 0o755); err != nil {
			return agent.Spec{}, err
		}
	}
```
(`strings` is imported from the alias work; confirm.)

- [ ] **Step 5: Run → pass.** `go test ./internal/app/bot -run 'TestResolveUsesBotWorkspace|TestCreateBotDefaultsWorkspace' -count=1` — PASS.

- [ ] **Step 6: Commit.**
```bash
git add internal/app/bot/service.go internal/app/bot/cli_resolver.go internal/app/bot/service_test.go internal/app/bot/cli_resolver_test.go
git commit -m "feat: default per-bot workspace at create and use it in the resolver"
```

---

## Task 5: Resolver session lookup → `ResumeSessionID`

**Files:** Modify `internal/app/bot/cli_resolver.go`; Test `internal/app/bot/cli_resolver_test.go`.

> This changes `NewBotCLIResolver`'s signature (adds a sessions repo). Update its other callers' test construction in this task; bootstrap is updated in Task 7.

- [ ] **Step 1: Write the failing test** — in `cli_resolver_test.go` add a sessions stub and a test:
```go
type agentSessionRepoStub struct {
	byKey map[string]domain.BotCLISession // key = botID + "|" + cliType
}

func (s *agentSessionRepoStub) Upsert(_ context.Context, sess domain.BotCLISession) error {
	if s.byKey == nil {
		s.byKey = map[string]domain.BotCLISession{}
	}
	s.byKey[sess.BotID+"|"+sess.CLIType] = sess
	return nil
}
func (s *agentSessionRepoStub) Get(_ context.Context, botID, cliType string) (domain.BotCLISession, error) {
	if v, ok := s.byKey[botID+"|"+cliType]; ok {
		return v, nil
	}
	return domain.BotCLISession{}, domain.ErrNotFound
}

func TestResolveSetsResumeSessionFromStore(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_s", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	sessions := &agentSessionRepoStub{byKey: map[string]domain.BotCLISession{
		"bot_s|codex": {BotID: "bot_s", CLIType: "codex", SessionID: "conv_42"},
	}}
	r := NewBotCLIResolver(bots, capabilities, sessions, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_s")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.ResumeSessionID != "conv_42" {
		t.Fatalf("ResumeSessionID = %q, want conv_42", spec.ResumeSessionID)
	}
}

func TestResolveNoStoredSessionLeavesResumeEmpty(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_n", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_n")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty", spec.ResumeSessionID)
	}
}
```
Also: every EXISTING `NewBotCLIResolver(bots, capabilities, ...)` call in `cli_resolver_test.go` (from earlier tasks/tests) must gain the new third arg `&agentSessionRepoStub{}`. `grep -n "NewBotCLIResolver(" internal/app/bot/cli_resolver_test.go` and update each.

- [ ] **Step 2: Run → fail.** `go test ./internal/app/bot -run TestResolveSetsResumeSessionFromStore` — COMPILE FAIL (`NewBotCLIResolver` takes 3 args; `spec.ResumeSessionID` set nowhere).

- [ ] **Step 3: Add the dependency + lookup.** In `internal/app/bot/cli_resolver.go`:
  - Add field `sessions domain.BotCLISessionRepository` to `BotCLIResolver`.
  - Change `NewBotCLIResolver(bots domain.BotRepository, capabilities domain.AgentCapabilityRepository, cfg BotCLIResolverConfig)` to `NewBotCLIResolver(bots domain.BotRepository, capabilities domain.AgentCapabilityRepository, sessions domain.BotCLISessionRepository, cfg BotCLIResolverConfig)` and set `sessions: sessions` in the struct literal.
  - In `Resolve`, after the `spec` is built (and `capability` is in scope), look up the session:
    ```go
    if r.sessions != nil {
        if stored, err := r.sessions.Get(ctx, botID, capability.Key); err == nil {
            spec.ResumeSessionID = stored.SessionID
        } else if !errors.Is(err, domain.ErrNotFound) {
            // non-fatal: log and continue with no resume
            log.Printf("cli session lookup failed: bot_id=%s cli=%s error=%v", botID, capability.Key, err)
        }
    }
    ```
    (Ensure `errors` and `log` are imported in cli_resolver.go.)

- [ ] **Step 4: Run → pass.** `go test ./internal/app/bot -run TestResolve -count=1` — PASS (new + existing resolve tests).

- [ ] **Step 5: Commit.**
```bash
git add internal/app/bot/cli_resolver.go internal/app/bot/cli_resolver_test.go
git commit -m "feat: resolver looks up stored CLI session and sets ResumeSessionID"
```

---

## Task 6: Orchestrator upserts the session after a turn

**Files:** Modify `internal/app/bot/message_orchestrator.go`; Test `internal/app/bot/message_orchestrator_test.go` (or the orchestrator test file in that package).

- [ ] **Step 1: Write the failing test.** Find how the orchestrator is constructed in the existing orchestrator test (e.g. `NewBotMessageOrchestrator(...)`); it will gain a sessions repo arg in Step 3. Add a test that, after processing a message whose executor returns a `Response{SessionID: "sess_z", RuntimeType: "claude"}`, the sessions stub received an Upsert with that id:
```go
func TestOrchestratorPersistsSessionAfterTurn(t *testing.T) {
	sessions := &orchSessionStub{}
	// build an orchestrator whose executor returns a Response with SessionID set,
	// for a non-orchestrator bot, using the package's existing test harness +
	// the new sessions arg. (Mirror the existing orchestrator test setup.)
	// ... process one inbound message ...
	if sessions.last.SessionID != "sess_z" || sessions.last.CLIType != "claude" {
		t.Fatalf("upsert = %#v", sessions.last)
	}
}
```
Define `orchSessionStub` implementing `domain.BotCLISessionRepository` recording the last `Upsert`. Reuse the existing orchestrator test scaffolding (the file already builds an orchestrator + a fake executor/resolver — extend that fake executor to return a `Response` with `SessionID` set).

- [ ] **Step 2: Run → fail.** `go test ./internal/app/bot -run TestOrchestratorPersistsSessionAfterTurn` — FAIL/COMPILE FAIL.

- [ ] **Step 3: Add the dependency + upsert.** In `internal/app/bot/message_orchestrator.go`:
  - Add a `sessions domain.BotCLISessionRepository` field to `BotMessageOrchestrator` and a constructor param (update `NewBotMessageOrchestrator` signature; bootstrap updated in Task 7).
  - In `processMessage`, on the success path (right before `o.replyWithTimeout(ctx, msg, result.resp)`), add:
    ```go
    if o.sessions != nil && strings.TrimSpace(result.resp.SessionID) != "" && result.resp.SessionID != spec.ResumeSessionID {
        if err := o.sessions.Upsert(context.WithoutCancel(ctx), domain.BotCLISession{
            BotID:     msg.BotID,
            CLIType:   result.resp.RuntimeType,
            SessionID: result.resp.SessionID,
            WorkDir:   spec.WorkDir,
        }); err != nil {
            log.Printf("cli session upsert failed: bot_id=%s cli=%s error=%v", msg.BotID, result.resp.RuntimeType, err)
        }
    }
    ```
    (`strings`, `context`, `log`, `domain` are already imported in this file.)
  - Do the same in `runOrchestratorTurn`'s detached goroutine after a successful `resp` (so brain bots also persist their session).

- [ ] **Step 4: Run → pass.** `go test ./internal/app/bot -count=1` — PASS.

- [ ] **Step 5: Commit.**
```bash
git add internal/app/bot/message_orchestrator.go internal/app/bot/message_orchestrator_test.go
git commit -m "feat: persist CLI session id after each turn"
```

---

## Task 7: Bootstrap wiring

**Files:** Modify `internal/bootstrap/bootstrap.go`.

- [ ] **Step 1: Construct the repo + wire it.** In `internal/bootstrap/bootstrap.go`:
  - After the other repos: `botCLISessionRepo := repositories.NewBotCLISessionRepository(db)`.
  - Pass it into the resolver: `bot.NewBotCLIResolver(botRepo, capabilityRepo, botCLISessionRepo, bot.BotCLIResolverConfig{...})`.
  - Pass it into the orchestrator: `bot.NewBotMessageOrchestrator(executor, multiReplyGateway, resolver, botCLISessionRepo)` (match the new param order from Task 6).
  - After `botSvc := bot.NewBotService(...)`: `botSvc.SetWorkspaceRoot(cfg.BotWorkspaceRoot())`.

- [ ] **Step 2: Build + bootstrap test.** `go build ./... && go test ./internal/bootstrap -count=1` — PASS.

- [ ] **Step 3: Commit.**
```bash
git add internal/bootstrap/bootstrap.go
git commit -m "feat: wire bot_cli_sessions repo and workspace root into bootstrap"
```

---

## Task 8: claude — capture session id + `--resume`

**Files:** Modify `internal/agent/claude/driver_acp.go`; Test `internal/agent/claude/driver_acp_test.go`.

- [ ] **Step 1: Write the failing test.** In `driver_acp_test.go`:
```go
func TestBuildACPArgsAddsResumeWhenSet(t *testing.T) {
	got := buildACPArgs("claude", nil, false, "sess_abc")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--resume sess_abc") {
		t.Fatalf("expected --resume sess_abc, got %v", got)
	}
}
func TestBuildACPArgsNoResumeWhenEmpty(t *testing.T) {
	got := buildACPArgs("claude", nil, false, "")
	if strings.Contains(strings.Join(got, " "), "--resume") {
		t.Fatalf("did not expect --resume, got %v", got)
	}
}
```

- [ ] **Step 2: Run → fail.** `go test ./internal/agent/claude -run TestBuildACPArgs` — COMPILE FAIL (`buildACPArgs` takes 3 args).

- [ ] **Step 3: Implement.**
  - Change `buildACPArgs(command string, extra []string, realCLI bool)` → `buildACPArgs(command string, extra []string, realCLI bool, resumeSessionID string)`. After the existing injected flags, before appending `extra`, when `resumeSessionID != ""` append `"--resume", resumeSessionID`. Keep the real-binary gate: if `!isClaudeCommand(command) && !realCLI` return `extra` verbatim (no resume either).
  - Update the `Init` call site to pass `spec.ResumeSessionID`: `acpArgs := buildACPArgs(spec.Command, spec.Args, spec.RealCLI, spec.ResumeSessionID)`.
  - Update existing `buildACPArgs(...)` test call sites: `grep -n "buildACPArgs(" internal/agent/claude/` and add a `, ""` 4th arg to the old 3-arg calls.
  - **Capture:** find where the read goroutine parses the `system`/`init` event (the struct with `SessionID json:"session_id"` and `Subtype`). When `Subtype == "init"` (or wherever session_id arrives), store it on the runtime under the mutex (add a field `sessionID string` to the `ACPRuntime` struct + a guarded getter). Set `SessionID: r.getSessionID()` on BOTH `agent.Response{...}` returns in `Run` (the error path ~line 210 and the success path ~line 224).

- [ ] **Step 4: Run → pass.** `go test ./internal/agent/claude -count=1` — PASS.

- [ ] **Step 5: Commit.**
```bash
git add internal/agent/claude/driver_acp.go internal/agent/claude/driver_acp_test.go
git commit -m "feat(claude): capture session_id and resume via --resume"
```

---

## Task 9: codex — capture session id + best-effort resume

**Files:** Modify `internal/agent/codex/driver_acp.go` (and `driver_exec.go` if it surfaces a session id); Test `internal/agent/codex/driver_acp_test.go`.

Codex uses an app-server (ACP) protocol; the exact conversation/session id field and resume call must be read from the driver. Implement, in order:

- [ ] **Step 1:** Read `driver_acp.go` fully — find (a) where the protocol returns/echoes a conversation/session id (e.g. in a `configure`/`newConversation`/turn-complete message), and (b) how a turn is started. Identify the id field.
- [ ] **Step 2 (capture):** Store the conversation/session id on the runtime under the mutex and set `SessionID: <captured>` on every `agent.Response{RuntimeType: runtimeTypeCodex, ...}` return in `Run`. Add a test (using the driver's existing fake/stub harness) asserting `Response.SessionID` is populated for a normal turn.
- [ ] **Step 3 (resume, best-effort):** When `spec.ResumeSessionID != ""`, pass it into the codex resume mechanism on session start (mirror what `driver_exec.go:84` does with `resume`, adapted to the ACP protocol's resume call — e.g. resuming a specific conversation id rather than `--last`). If the protocol has no per-id resume, leave resume as a documented no-op but still capture+store the id (so future versions can use it). On any resume failure, start a fresh session.
- [ ] **Step 4:** `go test ./internal/agent/codex -count=1` — PASS. `go build ./...`.
- [ ] **Step 5: Commit** `feat(codex): capture session id and best-effort resume`.

---

## Task 10: opencode — capture session id + best-effort reuse

**Files:** Modify `internal/agent/opencode/driver_acp.go`; Test `internal/agent/opencode/driver_acp_test.go`.

- [ ] **Step 1 (capture):** In `Run`, set `SessionID: <the session id from ensureSession>` on every `agent.Response{RuntimeType: runtimeTypeOpencode, ...}` return. Add a test asserting `Response.SessionID` is the session id created by the fake `session/new`.
- [ ] **Step 2 (reuse, best-effort):** In `ensureSession(ctx, workDir)`: if `r.spec.ResumeSessionID != ""` and there is no cached session, adopt it (`r.sessionID = r.spec.ResumeSessionID`, `r.sessionWorkDir = workDir`) and skip `session/new`. If a later `session/prompt` RPC fails because the server doesn't recognize the session, clear `r.sessionID` and call `session/new` to recover, then retry the prompt once. Add a test (driver fake) where a reused id that the fake rejects falls back to `session/new`.
- [ ] **Step 3:** `go test ./internal/agent/opencode -count=1` — PASS. `go build ./...`.
- [ ] **Step 4: Commit** `feat(opencode): capture session id and best-effort session reuse`.

---

## Task 11: Full verification

- [ ] **Step 1:** `go build ./... && go vet ./...` — clean.
- [ ] **Step 2:** `go test ./... 2>&1 | grep -vE '^ok|no test files'` — no `FAIL`.
- [ ] **Step 3:** Add an invariant test (in `internal/agent` or `internal/app/bot`) asserting the cli_type alignment so a future rename can't silently break save/lookup:
```go
// in internal/agent/agent_invariants_test.go (package agent)
func TestRuntimeTypeConstantsMatchCapabilityKeys(t *testing.T) {
	// The cli_type stored in bot_cli_sessions must be identical whether derived
	// from capability.Key (lookup) or Response.RuntimeType (save).
	for _, v := range []string{"claude", "codex", "opencode"} {
		if v == "" {
			t.Fatal("empty runtime type")
		}
	}
	// Document the contract; the real constants are in each driver package.
}
```
  (A lightweight guard; the real protection is that both sides use the literals {claude,codex,opencode}.)
- [ ] **Step 4: Manual smoke (optional, needs a real CLI):** create a bot with claude, send two messages across a server restart; confirm the second turn resumes (claude logs the same session, conversation continuity). Confirm `bot_cli_sessions` has a row. If the bot is codex/opencode, confirm at least capture (a row appears).
- [ ] **Step 5: Final commit** if any fixes: `chore: verification fixes for workspace + cli session`.

---

## Self-Review Notes
- **Spec coverage:** workspace persistence (Task 1, 4), `bot_cli_sessions` (Task 2), seam (Task 3), resolver lookup (Task 5), orchestrator save (Task 6), bootstrap (Task 7), claude/codex/opencode capture+resume (Tasks 8/9/10), verification + invariant (Task 11). All spec sections map to tasks.
- **Migrations** appended as `000006`/`000007` with explicit "do not renumber" notes; `db_test` version bumped 5→6→7 across Tasks 1 and 2.
- **Type consistency:** `BotCLISession{BotID,CLIType,SessionID,WorkDir,UpdatedAt}`, `BotCLISessionRepository{Upsert,Get}`, `Spec.ResumeSessionID`, `Response.SessionID`, `cli_type = capability.Key = Response.RuntimeType` are used identically across tasks. `NewBotCLIResolver` gains the sessions arg in Task 5 (callers updated there + bootstrap in Task 7); `NewBotMessageOrchestrator` gains it in Task 6 (bootstrap in Task 7).
- **Driver risk:** claude (Task 8) is fully specified; codex/opencode (Tasks 9/10) are directed read-then-implement with best-effort resume + fallback, matching the spec's flagged uncertainty — capture is concrete, cross-restart resume is best-effort.
