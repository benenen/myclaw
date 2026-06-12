# Brain–Subagent A2A Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "brain" orchestrator bot that decomposes/dispatches/monitors work across a fleet of registered sub-agents, where the brain (a normal claude-acp session) drives sub-agents through myclaw-provided MCP tools that bridge to an A2A task layer (local in-process, remote over HTTP).

**Architecture:** A bot with `Role=orchestrator` runs as a normal agent session via the existing `agent.Manager`. The resolver injects an MCP config + orchestrator system prompt + a large timeout. myclaw hosts an in-process MCP server (`/mcp`) exposing `list_agents`/`dispatch`/`get_task`/`cancel`; those tools read a `RegisteredAgent` registry and an in-memory Task store, and run sub-agents via a `Runner` that branches on `kind` (local → `Manager.Send`; remote → A2A HTTP client). Channel messages to the brain get an immediate ack, then the orchestration runs detached and the final answer is pushed back over the saved `ReplyTarget`.

**Tech Stack:** Go 1.23, net/http, GORM + SQLite, golang-migrate, `github.com/modelcontextprotocol/go-sdk` (MCP server + streamable HTTP), existing `internal/agent` driver/session/manager.

---

## Spec reference

Implements `docs/superpowers/specs/2026-06-12-brain-subagent-a2a-architecture-design.md` decisions D1–D12.

## Milestone map (each milestone leaves the tree green and testable)

- **M1 — Data model & registry**: `RegisteredAgent` entity/repo, `Bot.Role`, migration. (D5, D7, D10)
- **M2 — Task store**: in-memory task lifecycle. (D8)
- **M3 — Local runner + dispatcher core**: run a local sub-agent Bot via `Manager.Send`. (D11)
- **M4 — MCP server**: 4 tools over HTTP, mounted on the mux. (D4, D9)
- **M5 — Orchestrator role + ack/async + wiring**: resolver injection, `Spec.Orchestrator`, ack-then-push, bootstrap, system prompt, auto-register local sub-agents. **End of M5 = working local brain loop.** (D1, D3, D10, D12)
- **M6 — Remote A2A**: `POST /a2a/register` + heartbeat/TTL, A2A HTTP client runner, wiring `kind=remote`. (D6, D11)

M1–M5 are the shippable tracer bullet; M6 is purely additive on the `kind` seam.

## File structure (created/modified)

**Created**
- `internal/store/migrations/000004_registered_agents.up.sql` — table + `bots.role`
- `internal/store/models/registered_agent.go` — GORM model
- `internal/store/repositories/registered_agent_repository.go` — repo impl
- `internal/app/orchestration/task_store.go` — in-memory tasks
- `internal/app/orchestration/runner.go` — `Runner`, `LocalRunner`, interfaces
- `internal/app/orchestration/mcp.go` — MCP server + 4 tools + HTTP handler
- `internal/app/orchestration/prompt.go` + `internal/app/orchestration/prompt.md` — orchestrator system prompt (embedded)
- `internal/app/orchestration/registry.go` — local sub-agent auto-registration + name resolution helpers
- `internal/app/orchestration/a2a_client.go` (M6) — A2A JSON-RPC client
- `internal/app/orchestration/register_http.go` (M6) — `POST /a2a/register` + heartbeat handlers
- `internal/api/http/dto/a2a.go` (M6) — register/heartbeat DTOs

**Modified**
- `internal/domain/entities.go` — add `RegisteredAgent`, `Bot.Role`
- `internal/domain/repositories.go` — add `RegisteredAgentRepository`
- `internal/domain/status.go` — kind/health/role constants
- `internal/store/models/bot.go` — add `Role`
- `internal/store/repositories/bot_repository.go` — map `Role`
- `internal/agent/types.go` — add `Spec.Orchestrator`
- `internal/app/bot/cli_resolver.go` — orchestrator injection
- `internal/app/bot/message_orchestrator.go` — ack + async push path
- `internal/api/http/dto/bots.go` — add `Role` to bot DTOs
- `internal/api/http/handlers/bots.go` — pass `Role` through create/list
- `internal/app/bot/service.go` — accept `Role` on create
- `internal/config/config.go` — `OrchestratorTimeout`, `MCPURL`
- `internal/bootstrap/bootstrap.go` — wire everything
- `internal/api/http/handlers/router.go` (M6) — register routes
- `go.mod` / `go.sum` — add MCP SDK

---

# Milestone 1 — Data model & registry

### Task 1.1: Add domain constants for kind/health/role

**Files:**
- Modify: `internal/domain/status.go`

- [ ] **Step 1: Add constants**

Append to `internal/domain/status.go`:

```go
// Bot roles. Orchestrator bots are the "brain"; empty means a normal bot.
const (
	BotRoleOrchestrator = "orchestrator"
)

// Sub-agent types (Bot.Type) and registry kinds.
const (
	BotTypeSubagent = "subagent"

	RegisteredAgentKindLocal  = "local"
	RegisteredAgentKindRemote = "remote"

	RegisteredAgentHealthy   = "healthy"
	RegisteredAgentUnhealthy = "unhealthy"
)
```

- [ ] **Step 2: Build**

Run: `go build ./internal/domain/`
Expected: PASS (no output)

- [ ] **Step 3: Commit**

```bash
git add internal/domain/status.go
git commit -m "feat: add brain/subagent/registry domain constants"
```

---

### Task 1.2: Add `RegisteredAgent` entity and `Bot.Role`

**Files:**
- Modify: `internal/domain/entities.go`

- [ ] **Step 1: Add the entity + field**

Add `Role` to the `Bot` struct (after `AgentMode`):

```go
	AgentMode         string
	Role              string
```

Append the new entity at the end of `internal/domain/entities.go`:

```go
type RegisteredAgent struct {
	ID            string
	Name          string
	Description   string
	Kind          string
	BotID         string
	Endpoint      string
	AuthToken     string
	Health        string
	LastHeartbeat *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/domain/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/domain/entities.go
git commit -m "feat: add RegisteredAgent entity and Bot.Role"
```

---

### Task 1.3: Add `RegisteredAgentRepository` interface

**Files:**
- Modify: `internal/domain/repositories.go`

- [ ] **Step 1: Add interface**

Append to `internal/domain/repositories.go`:

```go
type RegisteredAgentRepository interface {
	Upsert(ctx context.Context, agent RegisteredAgent) (RegisteredAgent, error)
	GetByID(ctx context.Context, id string) (RegisteredAgent, error)
	GetByName(ctx context.Context, name string) (RegisteredAgent, error)
	List(ctx context.Context) ([]RegisteredAgent, error)
	DeleteByID(ctx context.Context, id string) error
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/domain/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/domain/repositories.go
git commit -m "feat: add RegisteredAgentRepository interface"
```

---

### Task 1.4: Migration — `registered_agents` table + `bots.role`

**Files:**
- Create: `internal/store/migrations/000004_registered_agents.up.sql`

- [ ] **Step 1: Write the migration**

```sql
CREATE TABLE IF NOT EXISTS registered_agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'local',
    bot_id TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL DEFAULT '',
    auth_token TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT 'healthy',
    last_heartbeat_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

ALTER TABLE bots ADD COLUMN role TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 2: Verify migrations still apply (uses in-memory sqlite)**

Run: `go test ./internal/store/repositories/ -run TestBot -count=1`
Expected: PASS (existing bot repo tests run `store.Migrate`, which now applies 000004)

- [ ] **Step 3: Commit**

```bash
git add internal/store/migrations/000004_registered_agents.up.sql
git commit -m "feat: migration for registered_agents table and bots.role"
```

---

### Task 1.5: GORM model + `Bot.Role` mapping

**Files:**
- Create: `internal/store/models/registered_agent.go`
- Modify: `internal/store/models/bot.go`
- Modify: `internal/store/repositories/bot_repository.go`

- [ ] **Step 1: Create the model**

`internal/store/models/registered_agent.go`:

```go
package models

import "time"

type RegisteredAgent struct {
	ID              string `gorm:"primaryKey"`
	Name            string `gorm:"not null;uniqueIndex"`
	Description     string `gorm:"not null;default:''"`
	Kind            string `gorm:"not null;default:'local'"`
	BotID           string `gorm:"not null;default:''"`
	Endpoint        string `gorm:"not null;default:''"`
	AuthToken       string `gorm:"not null;default:''"`
	Health          string `gorm:"not null;default:'healthy'"`
	LastHeartbeatAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (RegisteredAgent) TableName() string { return "registered_agents" }
```

- [ ] **Step 2: Add `Role` to the bot model**

In `internal/store/models/bot.go`, add after `AgentMode`:

```go
	AgentMode         string `gorm:"not null;default:''"`
	Role              string `gorm:"not null;default:''"`
```

- [ ] **Step 3: Map `Role` in the bot repository**

In `internal/store/repositories/bot_repository.go`, add `Role` to BOTH the model→domain conversion and the domain→model conversion (search for where `AgentMode` is copied and mirror it). Both the `models.Bot{...}` literal(s) and the `domain.Bot{...}` literal(s) must include `Role: <other>.Role`.

- [ ] **Step 4: Build + existing tests**

Run: `go build ./... && go test ./internal/store/... -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/models/registered_agent.go internal/store/models/bot.go internal/store/repositories/bot_repository.go
git commit -m "feat: RegisteredAgent model and Bot.Role mapping"
```

---

### Task 1.6: `RegisteredAgentRepository` implementation (TDD)

**Files:**
- Create: `internal/store/repositories/registered_agent_repository.go`
- Test: `internal/store/repositories/registered_agent_repository_test.go`

- [ ] **Step 1: Write the failing test**

Look at `internal/store/repositories/bot_repository_test.go` for the test DB helper (it opens in-memory sqlite + runs `store.Migrate`). Reuse the same helper function name it uses (e.g. `newTestDB(t)`).

`internal/store/repositories/registered_agent_repository_test.go`:

```go
package repositories

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

func TestRegisteredAgentUpsertByName(t *testing.T) {
	db := newTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	ctx := context.Background()

	in := domain.RegisteredAgent{
		ID:          domain.NewPrefixedID("ra"),
		Name:        "researcher",
		Description: "web research",
		Kind:        domain.RegisteredAgentKindLocal,
		BotID:       "bot_1",
		Health:      domain.RegisteredAgentHealthy,
	}
	got, err := repo.Upsert(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.Name != "researcher" || got.Kind != "local" || got.BotID != "bot_1" {
		t.Fatalf("unexpected stored agent: %+v", got)
	}

	// upsert again by same name updates description
	in.Description = "deep web research"
	if _, err := repo.Upsert(ctx, in); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	byName, err := repo.GetByName(ctx, "researcher")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.Description != "deep web research" {
		t.Fatalf("description not updated: %+v", byName)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(all))
	}
}

func TestRegisteredAgentGetByNameNotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	if _, err := repo.GetByName(context.Background(), "nope"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/repositories/ -run TestRegisteredAgent -count=1`
Expected: FAIL (undefined: `NewRegisteredAgentRepository`)

- [ ] **Step 3: Write the implementation**

`internal/store/repositories/registered_agent_repository.go`:

```go
package repositories

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RegisteredAgentRepository struct {
	db *gorm.DB
}

func NewRegisteredAgentRepository(db *gorm.DB) *RegisteredAgentRepository {
	return &RegisteredAgentRepository{db: db}
}

func (r *RegisteredAgentRepository) Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error) {
	now := time.Now().UTC()
	m := models.RegisteredAgent{
		ID:              a.ID,
		Name:            a.Name,
		Description:     a.Description,
		Kind:            a.Kind,
		BotID:           a.BotID,
		Endpoint:        a.Endpoint,
		AuthToken:       a.AuthToken,
		Health:          a.Health,
		LastHeartbeatAt: a.LastHeartbeat,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"description", "kind", "bot_id", "endpoint", "auth_token", "health", "last_heartbeat_at", "updated_at"}),
	}).Create(&m).Error; err != nil {
		return domain.RegisteredAgent{}, err
	}
	return r.GetByName(ctx, a.Name)
}

func (r *RegisteredAgentRepository) GetByID(ctx context.Context, id string) (domain.RegisteredAgent, error) {
	var m models.RegisteredAgent
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.RegisteredAgent{}, domain.ErrNotFound
		}
		return domain.RegisteredAgent{}, err
	}
	return toDomainRegisteredAgent(m), nil
}

func (r *RegisteredAgentRepository) GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error) {
	var m models.RegisteredAgent
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.RegisteredAgent{}, domain.ErrNotFound
		}
		return domain.RegisteredAgent{}, err
	}
	return toDomainRegisteredAgent(m), nil
}

func (r *RegisteredAgentRepository) List(ctx context.Context) ([]domain.RegisteredAgent, error) {
	var rows []models.RegisteredAgent
	if err := r.db.WithContext(ctx).Order("name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.RegisteredAgent, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDomainRegisteredAgent(row))
	}
	return items, nil
}

func (r *RegisteredAgentRepository) DeleteByID(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.RegisteredAgent{}).Error
}

func toDomainRegisteredAgent(m models.RegisteredAgent) domain.RegisteredAgent {
	return domain.RegisteredAgent{
		ID:            m.ID,
		Name:          m.Name,
		Description:   m.Description,
		Kind:          m.Kind,
		BotID:         m.BotID,
		Endpoint:      m.Endpoint,
		AuthToken:     m.AuthToken,
		Health:        m.Health,
		LastHeartbeat: m.LastHeartbeatAt,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

var _ domain.RegisteredAgentRepository = (*RegisteredAgentRepository)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/repositories/ -run TestRegisteredAgent -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/repositories/registered_agent_repository.go internal/store/repositories/registered_agent_repository_test.go
git commit -m "feat: RegisteredAgentRepository with upsert-by-name"
```

---

# Milestone 2 — Task store

### Task 2.1: In-memory Task store (TDD)

**Files:**
- Create: `internal/app/orchestration/task_store.go`
- Test: `internal/app/orchestration/task_store_test.go`

- [ ] **Step 1: Write the failing test**

`internal/app/orchestration/task_store_test.go`:

```go
package orchestration

import "testing"

func TestTaskStoreLifecycle(t *testing.T) {
	s := NewTaskStore()

	task := s.Create("researcher", "find X")
	if task.ID == "" || task.State != TaskStateSubmitted {
		t.Fatalf("unexpected created task: %+v", task)
	}

	s.SetWorking(task.ID)
	got, ok := s.Get(task.ID)
	if !ok || got.State != TaskStateWorking {
		t.Fatalf("expected working, got %+v ok=%v", got, ok)
	}

	s.Complete(task.ID, "the answer")
	got, _ = s.Get(task.ID)
	if got.State != TaskStateCompleted || got.Result != "the answer" {
		t.Fatalf("expected completed result, got %+v", got)
	}
}

func TestTaskStoreFailAndCancel(t *testing.T) {
	s := NewTaskStore()
	t1 := s.Create("a", "p")
	s.Fail(t1.ID, "boom")
	if got, _ := s.Get(t1.ID); got.State != TaskStateFailed || got.Error != "boom" {
		t.Fatalf("expected failed, got %+v", got)
	}

	t2 := s.Create("a", "p")
	if !s.Cancel(t2.ID) {
		t.Fatal("expected cancel to succeed for submitted task")
	}
	if got, _ := s.Get(t2.ID); got.State != TaskStateCanceled {
		t.Fatalf("expected canceled, got %+v", got)
	}
	// terminal tasks cannot be canceled
	if s.Cancel(t1.ID) {
		t.Fatal("expected cancel to fail for terminal task")
	}
}

func TestTaskStoreGetMissing(t *testing.T) {
	s := NewTaskStore()
	if _, ok := s.Get("nope"); ok {
		t.Fatal("expected missing task")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run TestTaskStore -count=1`
Expected: FAIL (package/types not defined)

- [ ] **Step 3: Write the implementation**

`internal/app/orchestration/task_store.go`:

```go
package orchestration

import (
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type TaskState string

const (
	TaskStateSubmitted TaskState = "submitted"
	TaskStateWorking   TaskState = "working"
	TaskStateCompleted TaskState = "completed"
	TaskStateFailed    TaskState = "failed"
	TaskStateCanceled  TaskState = "canceled"
)

func (s TaskState) terminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed || s == TaskStateCanceled
}

type Task struct {
	ID        string
	AgentName string
	Prompt    string
	State     TaskState
	Result    string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TaskStore struct {
	mu    sync.Mutex
	tasks map[string]*Task
	now   func() time.Time
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks: make(map[string]*Task),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *TaskStore) Create(agentName, prompt string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	t := &Task{
		ID:        domain.NewPrefixedID("task"),
		AgentName: agentName,
		Prompt:    prompt,
		State:     TaskStateSubmitted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.tasks[t.ID] = t
	return *t
}

func (s *TaskStore) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

func (s *TaskStore) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	return out
}

func (s *TaskStore) SetWorking(id string) { s.transition(id, TaskStateWorking, "", "") }

func (s *TaskStore) Complete(id, result string) { s.transition(id, TaskStateCompleted, result, "") }

func (s *TaskStore) Fail(id, errMsg string) { s.transition(id, TaskStateFailed, "", errMsg) }

func (s *TaskStore) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.State.terminal() {
		return false
	}
	t.State = TaskStateCanceled
	t.UpdatedAt = s.now()
	return true
}

func (s *TaskStore) transition(id string, state TaskState, result, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.State.terminal() {
		return
	}
	t.State = state
	if result != "" {
		t.Result = result
	}
	if errMsg != "" {
		t.Error = errMsg
	}
	t.UpdatedAt = s.now()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -run TestTaskStore -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/orchestration/task_store.go internal/app/orchestration/task_store_test.go
git commit -m "feat: in-memory orchestration task store"
```

---

# Milestone 3 — Local runner + dispatcher core

### Task 3.1: Runner interfaces + LocalRunner (TDD)

**Files:**
- Create: `internal/app/orchestration/runner.go`
- Test: `internal/app/orchestration/runner_test.go`

The runner is what `dispatch` calls to actually execute a sub-agent. Local agents run via the existing `agent.Manager` + spec resolver. Selection branches on `RegisteredAgent.Kind`; the remote branch is added in M6.

- [ ] **Step 1: Write the failing test**

`internal/app/orchestration/runner_test.go`:

```go
package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

type fakeResolver struct{ spec agent.Spec }

func (f fakeResolver) Resolve(ctx context.Context, botID string) (agent.Spec, error) {
	s := f.spec
	s.BotID = botID
	return s, nil
}

type fakeExecutor struct {
	gotBotID  string
	gotPrompt string
	resp      agent.Response
	err       error
}

func (f *fakeExecutor) Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error) {
	f.gotBotID = botID
	f.gotPrompt = req.Prompt
	return f.resp, f.err
}

func TestLocalRunnerSendsToBot(t *testing.T) {
	exec := &fakeExecutor{resp: agent.Response{Text: "done"}}
	r := NewRunner(NewLocalRunner(fakeResolver{}, exec), nil)

	out, err := r.Run(context.Background(), domain.RegisteredAgent{
		Kind:  domain.RegisteredAgentKindLocal,
		BotID: "bot_sub_1",
	}, "do the thing")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected output %q", out)
	}
	if exec.gotBotID != "bot_sub_1" || exec.gotPrompt != "do the thing" {
		t.Fatalf("executor got bot=%q prompt=%q", exec.gotBotID, exec.gotPrompt)
	}
}

func TestRunnerRejectsUnknownKind(t *testing.T) {
	r := NewRunner(nil, nil)
	if _, err := r.Run(context.Background(), domain.RegisteredAgent{Kind: "weird"}, "x"); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestLocalRunnerPropagatesError(t *testing.T) {
	exec := &fakeExecutor{err: errors.New("boom")}
	r := NewRunner(NewLocalRunner(fakeResolver{}, exec), nil)
	if _, err := r.Run(context.Background(), domain.RegisteredAgent{Kind: domain.RegisteredAgentKindLocal, BotID: "b"}, "x"); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run Runner -count=1`
Expected: FAIL (undefined `NewRunner`/`NewLocalRunner`)

- [ ] **Step 3: Write the implementation**

`internal/app/orchestration/runner.go`:

```go
package orchestration

import (
	"context"
	"fmt"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

// SpecResolver resolves a sub-agent Bot into an agent.Spec.
// Satisfied by *bot.BotCLIResolver.
type SpecResolver interface {
	Resolve(ctx context.Context, botID string) (agent.Spec, error)
}

// Executor runs one turn against a bot session. Satisfied by *agent.Manager.
type Executor interface {
	Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error)
}

// RemoteRunner is the A2A HTTP path; supplied in M6. nil until then.
type RemoteRunner interface {
	Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error)
}

type LocalRunner struct {
	resolver SpecResolver
	executor Executor
}

func NewLocalRunner(resolver SpecResolver, executor Executor) *LocalRunner {
	return &LocalRunner{resolver: resolver, executor: executor}
}

func (l *LocalRunner) Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error) {
	spec, err := l.resolver.Resolve(ctx, a.BotID)
	if err != nil {
		return "", fmt.Errorf("resolve sub-agent %s: %w", a.BotID, err)
	}
	resp, err := l.executor.Send(ctx, a.BotID, spec, agent.Request{
		BotID:     a.BotID,
		MessageID: domain.NewPrefixedID("subtask"),
		Prompt:    prompt,
	})
	if err != nil {
		return resp.Text, err
	}
	return resp.Text, nil
}

// Runner dispatches to the correct backend based on RegisteredAgent.Kind.
type Runner struct {
	local  *LocalRunner
	remote RemoteRunner
}

func NewRunner(local *LocalRunner, remote RemoteRunner) *Runner {
	return &Runner{local: local, remote: remote}
}

func (r *Runner) Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error) {
	switch a.Kind {
	case domain.RegisteredAgentKindLocal:
		if r.local == nil {
			return "", fmt.Errorf("local runner not configured")
		}
		return r.local.Run(ctx, a, prompt)
	case domain.RegisteredAgentKindRemote:
		if r.remote == nil {
			return "", fmt.Errorf("remote runner not configured")
		}
		return r.remote.Run(ctx, a, prompt)
	default:
		return "", fmt.Errorf("unknown agent kind %q", a.Kind)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -run Runner -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/orchestration/runner.go internal/app/orchestration/runner_test.go
git commit -m "feat: orchestration runner with local sub-agent execution"
```

---

# Milestone 4 — MCP server

### Task 4.1: Add the MCP Go SDK dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the module**

Run: `go get github.com/modelcontextprotocol/go-sdk/mcp@v1.0.0`
Expected: go.mod/go.sum updated.

- [ ] **Step 2: Verify it resolves**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add modelcontextprotocol/go-sdk"
```

---

### Task 4.2: MCP server with 4 tools (TDD)

**Files:**
- Create: `internal/app/orchestration/mcp.go`
- Test: `internal/app/orchestration/mcp_test.go`

The MCP server reads the registry + task store and dispatches via `Runner`. `dispatch` creates a task, runs the runner in a goroutine, and returns the task id immediately (async; D8). We expose the underlying tool functions as methods on an `MCPService` struct so they can be unit-tested directly without an MCP transport, then register them with the SDK in `NewMCPHandler`.

- [ ] **Step 1: Write the failing test**

`internal/app/orchestration/mcp_test.go`:

```go
package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type fakeRegistry struct{ agents []domain.RegisteredAgent }

func (f fakeRegistry) List(ctx context.Context) ([]domain.RegisteredAgent, error) {
	return f.agents, nil
}
func (f fakeRegistry) GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error) {
	for _, a := range f.agents {
		if a.Name == name {
			return a, nil
		}
	}
	return domain.RegisteredAgent{}, domain.ErrNotFound
}

func newTestService() (*MCPService, *fakeExecutor) {
	exec := &fakeExecutor{resp: agentResponse("sub-answer")}
	reg := fakeRegistry{agents: []domain.RegisteredAgent{{
		Name: "researcher", Description: "web", Kind: domain.RegisteredAgentKindLocal,
		BotID: "bot_sub", Health: domain.RegisteredAgentHealthy,
	}}}
	runner := NewRunner(NewLocalRunner(fakeResolver{}, exec), nil)
	svc := NewMCPService(reg, NewTaskStore(), runner)
	return svc, exec
}

func TestMCPListAgents(t *testing.T) {
	svc, _ := newTestService()
	out, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Agents) != 1 || out.Agents[0].Name != "researcher" {
		t.Fatalf("unexpected agents: %+v", out.Agents)
	}
}

func TestMCPDispatchThenGetTask(t *testing.T) {
	svc, _ := newTestService()
	disp, err := svc.Dispatch(context.Background(), DispatchInput{AgentName: "researcher", Prompt: "find X"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if disp.TaskID == "" {
		t.Fatal("expected task id")
	}

	// Poll until terminal (the runner runs in a goroutine).
	deadline := time.Now().Add(2 * time.Second)
	var got GetTaskOutput
	for time.Now().Before(deadline) {
		got, err = svc.GetTask(context.Background(), GetTaskInput{TaskID: disp.TaskID})
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if got.State == string(TaskStateCompleted) || got.State == string(TaskStateFailed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.State != string(TaskStateCompleted) || got.Result != "sub-answer" {
		t.Fatalf("unexpected final task: %+v", got)
	}
}

func TestMCPDispatchUnknownAgent(t *testing.T) {
	svc, _ := newTestService()
	if _, err := svc.Dispatch(context.Background(), DispatchInput{AgentName: "ghost", Prompt: "x"}); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}
```

Add this tiny helper to `runner_test.go` (used by the test above):

```go
func agentResponse(text string) agent.Response { return agent.Response{Text: text} }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run MCP -count=1`
Expected: FAIL (undefined `NewMCPService` etc.)

- [ ] **Step 3: Write the implementation**

`internal/app/orchestration/mcp.go`:

```go
package orchestration

import (
	"context"
	"fmt"
	"net/http"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registry is the read side of the agent registry the MCP tools need.
type Registry interface {
	List(ctx context.Context) ([]domain.RegisteredAgent, error)
	GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error)
}

type MCPService struct {
	registry Registry
	tasks    *TaskStore
	runner   *Runner
}

func NewMCPService(registry Registry, tasks *TaskStore, runner *Runner) *MCPService {
	return &MCPService{registry: registry, tasks: tasks, runner: runner}
}

// ---- tool I/O types (schemas are generated from these by the SDK) ----

type AgentInfo struct {
	Name        string `json:"name" jsonschema:"the dispatch name of the sub-agent"`
	Description string `json:"description" jsonschema:"what the sub-agent is good at"`
	Kind        string `json:"kind" jsonschema:"local or remote"`
	Health      string `json:"health" jsonschema:"healthy or unhealthy"`
}

type ListAgentsOutput struct {
	Agents []AgentInfo `json:"agents" jsonschema:"the available sub-agents"`
}

type DispatchInput struct {
	AgentName string `json:"agent_name" jsonschema:"name of the sub-agent to run (from list_agents)"`
	Prompt    string `json:"prompt" jsonschema:"the self-contained subtask instructions"`
}

type DispatchOutput struct {
	TaskID string `json:"task_id" jsonschema:"poll this id with get_task"`
}

type GetTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"a task id returned by dispatch"`
}

type GetTaskOutput struct {
	TaskID    string `json:"task_id"`
	AgentName string `json:"agent_name"`
	State     string `json:"state" jsonschema:"submitted, working, completed, failed, or canceled"`
	Result    string `json:"result" jsonschema:"final output when completed"`
	Error     string `json:"error" jsonschema:"error message when failed"`
}

type CancelInput struct {
	TaskID string `json:"task_id"`
}

type CancelOutput struct {
	Canceled bool `json:"canceled"`
}

// ---- tool implementations (testable without a transport) ----

func (s *MCPService) ListAgents(ctx context.Context) (ListAgentsOutput, error) {
	agents, err := s.registry.List(ctx)
	if err != nil {
		return ListAgentsOutput{}, err
	}
	out := ListAgentsOutput{Agents: make([]AgentInfo, 0, len(agents))}
	for _, a := range agents {
		if a.Health == domain.RegisteredAgentUnhealthy {
			continue
		}
		out.Agents = append(out.Agents, AgentInfo{
			Name: a.Name, Description: a.Description, Kind: a.Kind, Health: a.Health,
		})
	}
	return out, nil
}

func (s *MCPService) Dispatch(ctx context.Context, in DispatchInput) (DispatchOutput, error) {
	agent, err := s.registry.GetByName(ctx, in.AgentName)
	if err != nil {
		return DispatchOutput{}, fmt.Errorf("unknown agent %q: %w", in.AgentName, err)
	}
	task := s.tasks.Create(agent.Name, in.Prompt)
	go func() {
		s.tasks.SetWorking(task.ID)
		// Detached from the inbound request context so it outlives the tool call.
		result, runErr := s.runner.Run(context.Background(), agent, in.Prompt)
		if runErr != nil {
			s.tasks.Fail(task.ID, runErr.Error())
			return
		}
		s.tasks.Complete(task.ID, result)
	}()
	return DispatchOutput{TaskID: task.ID}, nil
}

func (s *MCPService) GetTask(ctx context.Context, in GetTaskInput) (GetTaskOutput, error) {
	t, ok := s.tasks.Get(in.TaskID)
	if !ok {
		return GetTaskOutput{}, fmt.Errorf("unknown task %q", in.TaskID)
	}
	return GetTaskOutput{
		TaskID: t.ID, AgentName: t.AgentName, State: string(t.State),
		Result: t.Result, Error: t.Error,
	}, nil
}

func (s *MCPService) CancelTask(ctx context.Context, in CancelInput) (CancelOutput, error) {
	return CancelOutput{Canceled: s.tasks.Cancel(in.TaskID)}, nil
}

// NewMCPHandler builds the MCP server, registers the four tools, and returns an
// http.Handler to mount (e.g. at /mcp).
func NewMCPHandler(svc *MCPService) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "myclaw", Version: "1.0.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_agents",
		Description: "List the sub-agents you can dispatch subtasks to.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListAgentsOutput, error) {
		out, err := svc.ListAgents(ctx)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "dispatch",
		Description: "Dispatch a self-contained subtask to a sub-agent. Returns a task_id immediately; poll it with get_task.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DispatchInput) (*mcp.CallToolResult, DispatchOutput, error) {
		out, err := svc.Dispatch(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_task",
		Description: "Get the current state and result of a dispatched task.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetTaskInput) (*mcp.CallToolResult, GetTaskOutput, error) {
		out, err := svc.GetTask(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cancel",
		Description: "Cancel a non-terminal task.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CancelInput) (*mcp.CallToolResult, CancelOutput, error) {
		out, err := svc.CancelTask(ctx, in)
		return nil, out, err
	})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: false})
}
```

> Note: the local variable named `agent` inside `Dispatch` shadows the `agent` package, but `Dispatch` does not reference that package, so it compiles. If you prefer, rename it `ra`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/orchestration/mcp.go internal/app/orchestration/mcp_test.go internal/app/orchestration/runner_test.go
git commit -m "feat: MCP server exposing list_agents/dispatch/get_task/cancel"
```

---

# Milestone 5 — Orchestrator role + ack/async + wiring

### Task 5.1: Add `Spec.Orchestrator` flag

**Files:**
- Modify: `internal/agent/types.go`

- [ ] **Step 1: Add the field**

In `internal/agent/types.go`, add to the `Spec` struct (after `QueueSize`):

```go
	QueueSize  int
	// Orchestrator marks a brain session: the orchestrator replies with an
	// immediate ack and runs the turn detached, pushing the final answer later.
	Orchestrator bool
```

- [ ] **Step 2: Build**

Run: `go build ./internal/agent/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/types.go
git commit -m "feat: add Spec.Orchestrator flag"
```

---

### Task 5.2: Config — orchestrator timeout + MCP URL

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add fields + loading**

Add to the `Config` struct:

```go
	ChannelMasterKey    []byte
	OrchestratorTimeout time.Duration
	MCPURL              string
```

Add `import "time"` and `"strconv"` if missing. In `Load()`, before `return cfg, nil` (after master key handling), add:

```go
	cfg.OrchestratorTimeout = 30 * time.Minute
	if v := os.Getenv("CHANNEL_ORCHESTRATOR_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse CHANNEL_ORCHESTRATOR_TIMEOUT: %w", err)
		}
		cfg.OrchestratorTimeout = d
	}

	cfg.MCPURL = getEnvOrDefault("CHANNEL_MCP_URL", "http://127.0.0.1"+cfg.HTTPAddr+"/mcp")
```

> `cfg.HTTPAddr` is like `:8080`, so the default URL becomes `http://127.0.0.1:8080/mcp`. Operators can override with `CHANNEL_MCP_URL` when the brain runs elsewhere.

- [ ] **Step 2: Build**

Run: `go build ./internal/config/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: config for orchestrator timeout and MCP URL"
```

---

### Task 5.3: Orchestrator system prompt asset

**Files:**
- Create: `internal/app/orchestration/prompt.md`
- Create: `internal/app/orchestration/prompt.go`

- [ ] **Step 1: Write the prompt**

`internal/app/orchestration/prompt.md`:

```markdown
You are the orchestrator ("brain"). A user message arrives through a channel and
you are responsible for delivering a single, complete final answer.

You do NOT do the work yourself. You decompose the request and delegate to
registered sub-agents using your MCP tools:

- `list_agents` — see which sub-agents exist and what each is good at.
- `dispatch(agent_name, prompt)` — give one sub-agent a self-contained subtask.
  Returns a `task_id` immediately. Each prompt must stand alone (the sub-agent
  has no shared context with you or other sub-agents).
- `get_task(task_id)` — poll a task until its state is `completed` or `failed`.
- `cancel(task_id)` — abandon a task you no longer need.

Loop:
1. Call `list_agents` to see the fleet.
2. Break the request into independent subtasks. Dispatch them (you may fan out
   several at once, then poll each).
3. Poll with `get_task` until tasks finish. If a task fails, decide whether to
   re-dispatch (possibly to a different agent) or proceed without it.
4. Synthesize the sub-agent results into ONE final answer addressed to the user.

Your final message text is what the user receives. Make it self-contained and
do not mention task ids or internal mechanics.
```

- [ ] **Step 2: Embed it**

`internal/app/orchestration/prompt.go`:

```go
package orchestration

import _ "embed"

//go:embed prompt.md
var orchestratorPrompt string

// OrchestratorPrompt returns the system prompt injected into brain sessions.
func OrchestratorPrompt() string { return orchestratorPrompt }
```

- [ ] **Step 3: Build**

Run: `go build ./internal/app/orchestration/`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/app/orchestration/prompt.md internal/app/orchestration/prompt.go
git commit -m "feat: orchestrator system prompt asset"
```

---

### Task 5.4: Resolver injects MCP config + prompt + timeout for orchestrator bots (TDD)

**Files:**
- Modify: `internal/app/bot/cli_resolver.go`
- Test: `internal/app/bot/cli_resolver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/app/bot/cli_resolver_test.go` (reuse its existing fakes/helpers for repos; mirror an existing resolver test for setup):

```go
func TestResolveOrchestratorInjectsMCPAndPrompt(t *testing.T) {
	// Arrange a bot with Role=orchestrator using the same in-memory setup
	// the other resolver tests use. capability must be Available with the
	// mode in SupportedModes.
	// (Follow the existing test's construction of repos + capability + bot,
	// then set bot.Role = domain.BotRoleOrchestrator before saving.)

	r := NewBotCLIResolver(botRepo, capabilityRepo, BotCLIResolverConfig{
		Timeout:             time.Minute,
		OrchestratorTimeout: 25 * time.Minute,
		MCPURL:              "http://127.0.0.1:8080/mcp",
		OrchestratorPrompt:  "BRAIN-PROMPT",
	})

	spec, err := r.Resolve(context.Background(), orchestratorBotID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !spec.Orchestrator {
		t.Fatal("expected Orchestrator=true")
	}
	if spec.Timeout != 25*time.Minute {
		t.Fatalf("expected orchestrator timeout, got %v", spec.Timeout)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--mcp-config") || !strings.Contains(joined, "/mcp") {
		t.Fatalf("expected mcp config in args: %v", spec.Args)
	}
	if !strings.Contains(joined, "--append-system-prompt") || !strings.Contains(joined, "BRAIN-PROMPT") {
		t.Fatalf("expected system prompt in args: %v", spec.Args)
	}
}
```

> Replace `botRepo`, `capabilityRepo`, `orchestratorBotID` with whatever the existing tests in this file use. Add `"strings"` and `"time"` to imports if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/bot/ -run TestResolveOrchestrator -count=1`
Expected: FAIL (fields `OrchestratorTimeout`/`MCPURL`/`OrchestratorPrompt` undefined)

- [ ] **Step 3: Implement**

In `internal/app/bot/cli_resolver.go`:

Add imports: `"encoding/json"`.

Extend the config struct:

```go
type BotCLIResolverConfig struct {
	Timeout             time.Duration
	WorkspaceRoot       string
	SQLitePath          string
	OrchestratorTimeout time.Duration
	MCPURL              string
	OrchestratorPrompt  string
}
```

Extend the resolver struct + constructor:

```go
type BotCLIResolver struct {
	bots                domain.BotRepository
	capabilities        domain.AgentCapabilityRepository
	timeout             time.Duration
	workspaceRoot       string
	sqlitePath          string
	orchestratorTimeout time.Duration
	mcpURL              string
	orchestratorPrompt  string
}

func NewBotCLIResolver(bots domain.BotRepository, capabilities domain.AgentCapabilityRepository, cfg BotCLIResolverConfig) *BotCLIResolver {
	return &BotCLIResolver{
		bots:                bots,
		capabilities:        capabilities,
		timeout:             cfg.Timeout,
		workspaceRoot:       cfg.WorkspaceRoot,
		sqlitePath:          cfg.SQLitePath,
		orchestratorTimeout: cfg.OrchestratorTimeout,
		mcpURL:              cfg.MCPURL,
		orchestratorPrompt:  cfg.OrchestratorPrompt,
	}
}
```

In `Resolve`, just before `return spec, nil`, insert:

```go
	if bot.Role == domain.BotRoleOrchestrator {
		spec.Orchestrator = true
		if r.orchestratorTimeout > 0 {
			spec.Timeout = r.orchestratorTimeout
		}
		extra := []string{}
		if r.mcpURL != "" {
			extra = append(extra, "--mcp-config", mcpConfigJSON(r.mcpURL))
		}
		if r.orchestratorPrompt != "" {
			extra = append(extra, "--append-system-prompt", r.orchestratorPrompt)
		}
		spec.Args = append(spec.Args, extra...)
	}
```

Add the helper at the end of the file:

```go
func mcpConfigJSON(url string) string {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"myclaw": map[string]any{
				"type": "http",
				"url":  url,
			},
		},
	}
	data, _ := json.Marshal(cfg)
	return string(data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/bot/ -run TestResolveOrchestrator -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/bot/cli_resolver.go internal/app/bot/cli_resolver_test.go
git commit -m "feat: resolver injects MCP config, prompt, timeout for orchestrator bots"
```

---

### Task 5.5: Orchestrator ack-then-async-push path (TDD)

**Files:**
- Modify: `internal/app/bot/message_orchestrator.go`
- Test: `internal/app/bot/message_orchestrator_test.go`

The brain must not hold the channel turn. For `spec.Orchestrator`, reply an ack immediately, run `executor.Send` in a detached goroutine, then push the final result over the saved `ReplyTarget`.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/bot/message_orchestrator_test.go` (reuse the file's existing fakes — there is already a fake executor/reply gateway/resolver used by other tests; match their names):

```go
func TestOrchestratorAcksThenPushesFinal(t *testing.T) {
	// resolver returns an orchestrator spec; executor returns the final answer.
	resolver := &fakeResolver{spec: agent.Spec{Orchestrator: true, Timeout: time.Minute}}
	exec := &fakeExecutor{resp: agent.Response{Text: "final answer"}}
	replies := &fakeReplyGateway{}

	o := NewBotMessageOrchestrator(exec, replies, resolver)
	o.HandleMessage(context.Background(), InboundMessage{
		BotID:     "brain_1",
		MessageID: "m1",
		From:      "user_1",
		Text:      "do a big thing",
	})

	// Expect: first reply is the ack, then (async) the final answer.
	waitForReplyCount(t, replies, 2)
	got := replies.texts()
	if got[0] != ackReply {
		t.Fatalf("expected ack first, got %q", got[0])
	}
	if got[len(got)-1] != "final answer" {
		t.Fatalf("expected final answer last, got %q", got[len(got)-1])
	}
}
```

> Implement `fakeResolver`, `fakeExecutor`, `fakeReplyGateway` if not already present, matching the `specResolver`/`executor`/`replyGateway` interfaces in `message_orchestrator.go`. `fakeReplyGateway` must be concurrency-safe (mutex) since the final reply comes from a goroutine; add helpers `texts()` and a test helper `waitForReplyCount(t, gw, n)` that polls up to ~2s.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/bot/ -run TestOrchestratorAcks -count=1`
Expected: FAIL (undefined `ackReply`, no async path)

- [ ] **Step 3: Implement**

In `internal/app/bot/message_orchestrator.go`:

Add to the const block:

```go
	ackReply = "收到，正在处理…"
```

In `processMessage`, immediately after the `spec` is resolved successfully and the queue is sized (after the `o.waitForQueueCapacity(...)` line, before constructing the synchronous `sendDone`), branch:

```go
	if spec.Orchestrator {
		o.runOrchestratorTurn(botID, msg, spec)
		return
	}
```

Add the new method:

```go
// runOrchestratorTurn acks immediately and runs the brain turn detached so the
// channel worker is freed; the final answer is pushed over the saved target.
func (o *BotMessageOrchestrator) runOrchestratorTurn(botID string, msg InboundMessage, spec agent.Spec) {
	o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})

	base := context.WithoutCancel(msg.Ctx)
	timeout := spec.Timeout
	go func() {
		ctx := base
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(base, timeout)
			defer cancel()
		}
		resp, err := o.executor.Send(ctx, msg.BotID, spec, agent.Request{
			BotID:     msg.BotID,
			UserID:    msg.From,
			MessageID: msg.MessageID,
			Prompt:    msg.Text,
		})
		if err != nil {
			replyText := failedReply
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				replyText = timeoutReply
			}
			o.replyWithTimeout(ctx, msg, agent.Response{Text: replyText})
			o.finishMessageEventually(msg, false)
			return
		}
		o.replyWithTimeout(ctx, msg, resp)
		o.finishMessageEventually(msg, true)
	}()
}
```

> `context.WithoutCancel` requires Go 1.21+ (project is on 1.23). `markMessageStarted` was already called at the top of `processMessage`; the goroutine owns `finishMessageEventually`, so dedup stays `inProgress` until the brain finishes and `reclaimWorker` won't reclaim while `active > 0`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/bot/ -run TestOrchestrator -count=1 && go test ./internal/app/bot/ -count=1`
Expected: PASS (new test + no regressions)

- [ ] **Step 5: Commit**

```bash
git add internal/app/bot/message_orchestrator.go internal/app/bot/message_orchestrator_test.go
git commit -m "feat: orchestrator bots ack then push final answer asynchronously"
```

---

### Task 5.6: Thread `Role` through DTO / service / create handler

**Files:**
- Modify: `internal/api/http/dto/bots.go`
- Modify: `internal/app/bot/service.go`
- Modify: `internal/api/http/handlers/bots.go`

- [ ] **Step 1: Add `Role` to DTOs**

In `internal/api/http/dto/bots.go`, add `Role string \`json:"role,omitempty"\`` to `CreateBotRequest`, `CreateBotResponse`, and `BotResponse` (mirror the `Type` field placement).

- [ ] **Step 2: Accept `Role` in the service create input**

In `internal/app/bot/service.go`, find `CreateBotInput` and the bot it builds; add a `Role string` field to `CreateBotInput` and set `Role: input.Role` on the `domain.Bot` it creates. Also include `Role` in any output struct it returns (mirror `Type`).

- [ ] **Step 3: Pass `Role` through the handler**

In `internal/api/http/handlers/bots.go`, in `CreateBot`, add `Role: req.Role,` to the `botapp.CreateBotInput{...}`, and add `Role: result.Role,` to the `dto.CreateBotResponse{...}`. In `ListBots`, add `Role: item.Role,` to each `dto.BotResponse{...}`.

- [ ] **Step 4: Build + tests**

Run: `go build ./... && go test ./internal/app/bot/ ./internal/api/... -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/http/dto/bots.go internal/app/bot/service.go internal/api/http/handlers/bots.go
git commit -m "feat: thread Bot.Role through API and service"
```

---

### Task 5.7: Local sub-agent auto-registration (TDD)

**Files:**
- Create: `internal/app/orchestration/registry.go`
- Test: `internal/app/orchestration/registry_test.go`

On startup, every bot with `Type=subagent` is upserted into the registry as `kind=local`, named by the bot name, pointing at its bot id.

- [ ] **Step 1: Write the failing test**

`internal/app/orchestration/registry_test.go`:

```go
package orchestration

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

type fakeBotLister struct{ bots []domain.Bot }

func (f fakeBotLister) ListWithAccounts(ctx context.Context) ([]domain.Bot, error) {
	return f.bots, nil
}

type recordingRegistry struct{ upserts []domain.RegisteredAgent }

func (r *recordingRegistry) Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error) {
	r.upserts = append(r.upserts, a)
	return a, nil
}

func TestSyncLocalAgentsRegistersSubagents(t *testing.T) {
	bots := fakeBotLister{bots: []domain.Bot{
		{ID: "bot_a", Name: "researcher", Type: domain.BotTypeSubagent},
		{ID: "bot_b", Name: "channel-bot", Type: "channel"},
	}}
	reg := &recordingRegistry{}

	if err := SyncLocalAgents(context.Background(), bots, reg); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(reg.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(reg.upserts))
	}
	got := reg.upserts[0]
	if got.Name != "researcher" || got.Kind != domain.RegisteredAgentKindLocal || got.BotID != "bot_a" {
		t.Fatalf("unexpected upsert: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run SyncLocal -count=1`
Expected: FAIL (undefined `SyncLocalAgents`)

- [ ] **Step 3: Implement**

`internal/app/orchestration/registry.go`:

```go
package orchestration

import (
	"context"

	"github.com/benenen/myclaw/internal/domain"
)

// BotLister is the slice of the bot repo we need for auto-registration.
type BotLister interface {
	ListWithAccounts(ctx context.Context) ([]domain.Bot, error)
}

// Upserter is the write side of the registry.
type Upserter interface {
	Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error)
}

// SyncLocalAgents registers every Type=subagent bot as a local registry entry.
func SyncLocalAgents(ctx context.Context, bots BotLister, reg Upserter) error {
	all, err := bots.ListWithAccounts(ctx)
	if err != nil {
		return err
	}
	for _, b := range all {
		if b.Type != domain.BotTypeSubagent {
			continue
		}
		if _, err := reg.Upsert(ctx, domain.RegisteredAgent{
			ID:          domain.NewPrefixedID("ra"),
			Name:        b.Name,
			Description: b.Name,
			Kind:        domain.RegisteredAgentKindLocal,
			BotID:       b.ID,
			Health:      domain.RegisteredAgentHealthy,
		}); err != nil {
			return err
		}
	}
	return nil
}
```

> Upsert is by `name`, so re-running on restart updates rather than duplicates. A fresh `ID` on each call is fine — the conflict clause keeps the existing row's identity via the `name` unique index. (If the repo's `OnConflict` returns the stored row, the new ID is ignored.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/orchestration/registry.go internal/app/orchestration/registry_test.go
git commit -m "feat: auto-register local sub-agent bots into the registry"
```

---

### Task 5.8: Wire orchestration into bootstrap

**Files:**
- Modify: `internal/bootstrap/bootstrap.go`

- [ ] **Step 1: Add the wiring**

Add imports:

```go
	"github.com/benenen/myclaw/internal/app/orchestration"
```

After `capabilityRepo := repositories.NewAgentCapabilityRepository(db)` add:

```go
	registeredAgentRepo := repositories.NewRegisteredAgentRepository(db)
```

Change the resolver construction to pass the new config:

```go
	resolver := bot.NewBotCLIResolver(botRepo, capabilityRepo, bot.BotCLIResolverConfig{
		Timeout:             botCLITimeout,
		WorkspaceRoot:       cfg.BotWorkspaceRoot(),
		SQLitePath:          cfg.SQLitePath,
		OrchestratorTimeout: cfg.OrchestratorTimeout,
		MCPURL:              cfg.MCPURL,
		OrchestratorPrompt:  orchestration.OrchestratorPrompt(),
	})
```

After the `orchestrator := bot.NewBotMessageOrchestrator(...)` line, add the orchestration stack:

```go
	taskStore := orchestration.NewTaskStore()
	localRunner := orchestration.NewLocalRunner(resolver, executor)
	runner := orchestration.NewRunner(localRunner, nil) // remote runner added in M6
	mcpService := orchestration.NewMCPService(registeredAgentRepo, taskStore, runner)
```

After `mux.Handle("/", web.Handler())` add the MCP mount (before `handlers.RegisterRoutes`):

```go
	mux.Handle("/mcp", orchestration.NewMCPHandler(mcpService))
	mux.Handle("/mcp/", orchestration.NewMCPHandler(mcpService))
```

After the capability `discoverer.Refresh(...)` block, register local sub-agents:

```go
	if err := orchestration.SyncLocalAgents(context.Background(), botRepo, registeredAgentRepo); err != nil {
		logger.Info("local sub-agent registration failed", "error", err)
	}
```

- [ ] **Step 2: Build + full test suite**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/bootstrap/bootstrap.go
git commit -m "feat: wire MCP server, task store, runner, and local agent sync"
```

---

### Task 5.9: Manual end-to-end smoke (local brain loop)

**Files:** none (verification only)

- [ ] **Step 1: Start the server**

```bash
export CHANNEL_MASTER_KEY=$(openssl rand -base64 32)
go run ./cmd/server
```

- [ ] **Step 2: Verify MCP endpoint is mounted**

In another terminal:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
```

Expected: `200` (an MCP initialize response; non-200 means the handler isn't mounted).

- [ ] **Step 3: Confirm in the UI**

Create a sub-agent bot (`type=subagent`, give it a claude/codex capability+mode) and a brain bot (you will set its `role=orchestrator` — until the UI exposes role, set it directly: `sqlite3 ~/.myclaw/myclaw.db "UPDATE bots SET role='orchestrator' WHERE name='<brain>';"`), then send the brain a message via `POST /api/v1/bots/simulate-message` and confirm you receive an immediate ack followed by a final answer.

- [ ] **Step 4: Commit (docs only, if you captured notes)**

No code change required; M5 is complete when the loop works.

---

# Milestone 6 — Remote A2A agents

> Additive: only `kind=remote` paths are new. Local loop from M5 keeps working untouched.

### Task 6.1: A2A JSON-RPC client runner (TDD)

**Files:**
- Create: `internal/app/orchestration/a2a_client.go`
- Test: `internal/app/orchestration/a2a_client_test.go`

A2A `message/send` is a JSON-RPC 2.0 POST to the agent's endpoint. v1 uses the blocking form and reads text parts from the returned message/task.

- [ ] **Step 1: Write the failing test**

`internal/app/orchestration/a2a_client_test.go`:

```go
package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

func TestA2AClientMessageSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "message/send" {
			t.Errorf("unexpected method %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]any{
				"kind": "message",
				"role": "agent",
				"parts": []map[string]any{
					{"kind": "text", "text": "remote answer"},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewA2AClient(srv.Client())
	out, err := c.Run(context.Background(), domain.RegisteredAgent{
		Kind: domain.RegisteredAgentKindRemote, Endpoint: srv.URL,
	}, "do remote work")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "remote answer" {
		t.Fatalf("unexpected output %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run A2A -count=1`
Expected: FAIL (undefined `NewA2AClient`)

- [ ] **Step 3: Implement**

`internal/app/orchestration/a2a_client.go`:

```go
package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/benenen/myclaw/internal/domain"
)

type A2AClient struct {
	http *http.Client
}

func NewA2AClient(httpClient *http.Client) *A2AClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &A2AClient{http: httpClient}
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type a2aMessage struct {
	Kind      string    `json:"kind"`
	Role      string    `json:"role"`
	MessageID string    `json:"messageId"`
	Parts     []a2aPart `json:"parts"`
}

type a2aRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type a2aResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *A2AClient) Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error) {
	reqBody := a2aRequest{
		JSONRPC: "2.0",
		ID:      domain.NewPrefixedID("rpc"),
		Method:  "message/send",
		Params: map[string]any{
			"message": a2aMessage{
				Kind:      "message",
				Role:      "user",
				MessageID: domain.NewPrefixedID("msg"),
				Parts:     []a2aPart{{Kind: "text", Text: prompt}},
			},
		},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.AuthToken)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("a2a request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("a2a endpoint returned %d", resp.StatusCode)
	}

	var rpc a2aResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return "", fmt.Errorf("decode a2a response: %w", err)
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("a2a error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return extractText(rpc.Result)
}

// extractText pulls text parts from either a Message result or a Task result
// (status.message or artifacts), covering the common A2A response shapes.
func extractText(raw json.RawMessage) (string, error) {
	var msg a2aMessage
	if err := json.Unmarshal(raw, &msg); err == nil && len(msg.Parts) > 0 {
		return joinTextParts(msg.Parts), nil
	}
	var task struct {
		Status struct {
			Message a2aMessage `json:"message"`
		} `json:"status"`
		Artifacts []struct {
			Parts []a2aPart `json:"parts"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &task); err != nil {
		return "", fmt.Errorf("unrecognized a2a result: %w", err)
	}
	if len(task.Status.Message.Parts) > 0 {
		return joinTextParts(task.Status.Message.Parts), nil
	}
	for _, art := range task.Artifacts {
		if len(art.Parts) > 0 {
			return joinTextParts(art.Parts), nil
		}
	}
	return "", nil
}

func joinTextParts(parts []a2aPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

var _ RemoteRunner = (*A2AClient)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -run A2A -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/orchestration/a2a_client.go internal/app/orchestration/a2a_client_test.go
git commit -m "feat: A2A JSON-RPC client for remote sub-agents"
```

---

### Task 6.2: `POST /a2a/register` + heartbeat handlers (TDD)

**Files:**
- Create: `internal/api/http/dto/a2a.go`
- Create: `internal/app/orchestration/register_http.go`
- Test: `internal/app/orchestration/register_http_test.go`

- [ ] **Step 1: DTOs**

`internal/api/http/dto/a2a.go`:

```go
package dto

type RegisterAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
	AuthToken   string `json:"auth_token,omitempty"`
}

type RegisterAgentResponse struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type HeartbeatRequest struct {
	Name string `json:"name"`
}
```

- [ ] **Step 2: Write the failing test**

`internal/app/orchestration/register_http_test.go`:

```go
package orchestration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

func TestRegisterHandlerUpsertsRemoteAgent(t *testing.T) {
	reg := &recordingRegistry{}
	h := RegisterHandler(reg)

	req := httptest.NewRequest(http.MethodPost, "/a2a/register",
		strings.NewReader(`{"name":"weatherbot","description":"weather","endpoint":"http://x:9000/a2a"}`))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(reg.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(reg.upserts))
	}
	got := reg.upserts[0]
	if got.Kind != domain.RegisteredAgentKindRemote || got.Endpoint != "http://x:9000/a2a" || got.Health != domain.RegisteredAgentHealthy {
		t.Fatalf("unexpected upsert: %+v", got)
	}
	_ = context.Background()
}

func TestRegisterHandlerRejectsMissingFields(t *testing.T) {
	h := RegisterHandler(&recordingRegistry{})
	req := httptest.NewRequest(http.MethodPost, "/a2a/register", strings.NewReader(`{"name":""}`))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK { // envelope-style: still 200 but code != OK
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"code":"OK"`) {
		t.Fatalf("expected non-OK envelope, got %s", rec.Body.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run Register -count=1`
Expected: FAIL (undefined `RegisterHandler`)

- [ ] **Step 4: Implement**

`internal/app/orchestration/register_http.go`:

```go
package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	"github.com/benenen/myclaw/internal/domain"
)

// RegisterHandler upserts a remote sub-agent from its self-registration call.
func RegisterHandler(reg Upserter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.RegisterAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Endpoint) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "name and endpoint are required")
			return
		}
		now := time.Now().UTC()
		stored, err := reg.Upsert(r.Context(), domain.RegisteredAgent{
			ID:            domain.NewPrefixedID("ra"),
			Name:          req.Name,
			Description:   req.Description,
			Kind:          domain.RegisteredAgentKindRemote,
			Endpoint:      req.Endpoint,
			AuthToken:     req.AuthToken,
			Health:        domain.RegisteredAgentHealthy,
			LastHeartbeat: &now,
		})
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		httpapi.WriteOKFromRequest(w, r, dto.RegisterAgentResponse{AgentID: stored.ID, Name: stored.Name})
	}
}

// HeartbeatHandler refreshes a remote agent's health/last-heartbeat.
func HeartbeatHandler(reg interface {
	Upserter
	GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error)
}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "name is required")
			return
		}
		existing, err := reg.GetByName(r.Context(), req.Name)
		if err != nil {
			httpapi.WriteError(w, r, "NOT_FOUND", "agent not registered")
			return
		}
		now := time.Now().UTC()
		existing.Health = domain.RegisteredAgentHealthy
		existing.LastHeartbeat = &now
		if _, err := reg.Upsert(r.Context(), existing); err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		httpapi.WriteOKFromRequest(w, r, map[string]any{"ok": true})
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -run Register -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/http/dto/a2a.go internal/app/orchestration/register_http.go internal/app/orchestration/register_http_test.go
git commit -m "feat: A2A remote agent register + heartbeat HTTP handlers"
```

---

### Task 6.3: Heartbeat-expiry janitor (TDD)

**Files:**
- Create: `internal/app/orchestration/health.go`
- Test: `internal/app/orchestration/health_test.go`

Mark remote agents whose last heartbeat is older than a TTL as `unhealthy` so `list_agents` filters them.

- [ ] **Step 1: Write the failing test**

`internal/app/orchestration/health_test.go`:

```go
package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type listUpsertRegistry struct {
	agents  []domain.RegisteredAgent
	updated []domain.RegisteredAgent
}

func (r *listUpsertRegistry) List(ctx context.Context) ([]domain.RegisteredAgent, error) {
	return r.agents, nil
}
func (r *listUpsertRegistry) Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error) {
	r.updated = append(r.updated, a)
	return a, nil
}

func TestSweepStaleRemoteAgents(t *testing.T) {
	old := time.Now().UTC().Add(-10 * time.Minute)
	fresh := time.Now().UTC()
	reg := &listUpsertRegistry{agents: []domain.RegisteredAgent{
		{Name: "stale", Kind: domain.RegisteredAgentKindRemote, Health: domain.RegisteredAgentHealthy, LastHeartbeat: &old},
		{Name: "ok", Kind: domain.RegisteredAgentKindRemote, Health: domain.RegisteredAgentHealthy, LastHeartbeat: &fresh},
		{Name: "local", Kind: domain.RegisteredAgentKindLocal, Health: domain.RegisteredAgentHealthy},
	}}

	n, err := SweepStaleAgents(context.Background(), reg, 5*time.Minute, func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 || len(reg.updated) != 1 || reg.updated[0].Name != "stale" || reg.updated[0].Health != domain.RegisteredAgentUnhealthy {
		t.Fatalf("expected only stale marked unhealthy, got n=%d updated=%+v", n, reg.updated)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/orchestration/ -run Sweep -count=1`
Expected: FAIL (undefined `SweepStaleAgents`)

- [ ] **Step 3: Implement**

`internal/app/orchestration/health.go`:

```go
package orchestration

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

// ListUpserter is the read+write registry slice the sweeper needs.
type ListUpserter interface {
	List(ctx context.Context) ([]domain.RegisteredAgent, error)
	Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error)
}

// SweepStaleAgents marks healthy remote agents whose heartbeat is older than ttl
// as unhealthy. Returns the number marked. Local agents are never swept.
func SweepStaleAgents(ctx context.Context, reg ListUpserter, ttl time.Duration, now func() time.Time) (int, error) {
	agents, err := reg.List(ctx)
	if err != nil {
		return 0, err
	}
	marked := 0
	for _, a := range agents {
		if a.Kind != domain.RegisteredAgentKindRemote || a.Health != domain.RegisteredAgentHealthy {
			continue
		}
		if a.LastHeartbeat == nil || now().Sub(*a.LastHeartbeat) <= ttl {
			continue
		}
		a.Health = domain.RegisteredAgentUnhealthy
		if _, err := reg.Upsert(ctx, a); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// StartHealthSweeper runs SweepStaleAgents on an interval until ctx is done.
func StartHealthSweeper(ctx context.Context, reg ListUpserter, ttl, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = SweepStaleAgents(ctx, reg, ttl, func() time.Time { return time.Now().UTC() })
			}
		}
	}()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/orchestration/ -run Sweep -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/orchestration/health.go internal/app/orchestration/health_test.go
git commit -m "feat: heartbeat-expiry sweeper for remote agents"
```

---

### Task 6.4: Wire remote runner, register routes, start sweeper

**Files:**
- Modify: `internal/bootstrap/bootstrap.go`
- Modify: `internal/api/http/handlers/router.go`

- [ ] **Step 1: Use the remote runner**

In `internal/bootstrap/bootstrap.go`, change the runner construction:

```go
	runner := orchestration.NewRunner(localRunner, orchestration.NewA2AClient(nil))
```

Mount the register/heartbeat routes (after the `/mcp` mounts):

```go
	mux.Handle("POST /a2a/register", orchestration.RegisterHandler(registeredAgentRepo))
	mux.Handle("POST /a2a/heartbeat", orchestration.HeartbeatHandler(registeredAgentRepo))
```

Start the sweeper (after `SyncLocalAgents`):

```go
	orchestration.StartHealthSweeper(context.Background(), registeredAgentRepo, 90*time.Second, 30*time.Second)
```

> `registeredAgentRepo` satisfies `orchestration.Registry`, `Upserter`, and `ListUpserter` because `*RegisteredAgentRepository` has `List`, `Upsert`, and `GetByName`.

- [ ] **Step 2: Build + full suite**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/bootstrap/bootstrap.go internal/api/http/handlers/router.go
git commit -m "feat: wire remote A2A runner, register endpoints, and health sweeper"
```

---

## Final verification

- [ ] `go build ./...` — PASS
- [ ] `go test ./... -count=1` — PASS
- [ ] Manual: register a remote agent via `curl -X POST localhost:8080/a2a/register -d '{"name":"echo","endpoint":"http://...","description":"echo"}'`, confirm it appears in the brain's `list_agents` and that `dispatch` to it round-trips through the A2A client.

---

## Self-review notes (author)

- **Spec coverage:** D1 (T5.4/5.5/5.8 — brain reuses Manager), D3 (T4.2/5.8 — myclaw hosts), D4 (M4 — MCP tools bridge), D5 (T1.2/5.7 — Bot subagent + name/description), D6 (T6.2 — remote register), D7 (T1.5/1.6 — RegisteredAgent+kind), D8 (M2 + T4.2 async), D9 (T4.2/5.8 — HTTP/SSE MCP), D10 (T5.4 — Role + injection), D11 (M3 + T6.1 — local/remote split), D12 (T5.5 — ack+push). Heartbeat/TTL: T6.3.
- **Deferred per spec v1:** progress streaming to channel, myclaw-as-A2A-server, Task persistence (in-memory only), monitoring UI, full auth (bearer token only).
- **Known risks carried from spec:** `context_token` expiry on long async pushes (push fails → logged); single brain session shared across users; brain session concurrency is serialized by `agent.Session` (each detached turn for the same brain bot contends on the session lock — acceptable for v1, revisit if multiple concurrent users hit one brain).
- **Type consistency check:** `TaskState` constants, `RegisteredAgent` fields, `Runner`/`LocalRunner`/`RemoteRunner` signatures, and MCP I/O structs are referenced identically across M2–M6. `registeredAgentRepo` implements every orchestration interface it is passed to.
