# Per-Bot MCP Server Attachment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Maintain a catalog of MCP servers and attach any subset to each bot, injecting only an orchestrator bot's attached+enabled servers into its agent `--mcp-config`.

**Architecture:** A `mcp_servers` catalog table + a `bot_mcp_servers` M:N join table. A `domain.MCPServerRepository` owns both (catalog CRUD + join queries). An app-layer `mcpserver.Service` validates and is used by the `myclaw mcp` CLI; `BotService` uses the same repo for the web path; `BotCLIResolver` reads `ListEnabledByBot` at launch.

**Tech Stack:** Go 1.23, GORM + SQLite, golang-migrate (embedded `.up.sql`), cobra CLI, vanilla JS frontend.

Spec: `docs/superpowers/specs/2026-06-22-per-bot-mcp-servers-design.md`.

---

## File Structure

- Create `internal/store/migrations/000008_mcp_servers.up.sql` — both tables.
- Modify `internal/store/db_test.go` — version assertion 7 → 8.
- Modify `internal/domain/entities.go` — `MCPServer` entity.
- Modify `internal/domain/repositories.go` — `MCPServerRepository` interface.
- Create `internal/store/models/mcp_server.go` — `MCPServer` + `BotMCPServer` GORM models.
- Create `internal/store/repositories/mcp_server_repository.go` — impl (catalog + join + cascade).
- Create `internal/store/repositories/mcp_server_repository_test.go` — repo tests.
- Create `internal/app/mcpserver/service.go` — validation + attach/detach.
- Create `internal/app/mcpserver/service_test.go` — service tests.
- Modify `internal/app/bot/cli_resolver.go` — `buildMCPConfigJSON(ctx, botID)`.
- Modify `internal/app/bot/cli_resolver_test.go` — resolver tests.
- Modify `internal/app/bot/service.go` — inject repo; persist + echo attachments; `ListMCPServers`.
- Modify `internal/api/http/dto/bots.go` — `mcp_server_ids`; add `MCPServerResponse`.
- Modify `internal/api/http/handlers/bots.go` — pass ids; `ListMCPServers` handler.
- Modify `internal/api/http/handlers/router.go` — register `GET /api/v1/mcp-servers`.
- Modify `internal/api/http/handlers/bots_test.go` — handler test.
- Create `cmd/mcp.go` — the `mcp` command tree.
- Modify `main.go` — register `mcp` command + usage line.
- Modify `internal/bootstrap/bootstrap.go` — wire repo into resolver + BotService.
- Modify `internal/api/http/web/static/index.html` + `app.js` — attachment multi-select.

---

## Task 1: Migration + schema version

**Files:**
- Create: `internal/store/migrations/000008_mcp_servers.up.sql`
- Modify: `internal/store/db_test.go` (the two `version != 7` assertions)

- [ ] **Step 1: Update the version assertions to fail**

In `internal/store/db_test.go`, change both occurrences:
```go
	if version != 8 {
		t.Fatalf("unexpected schema version: %d", version)
	}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store -run TestMigrate -v`
Expected: FAIL — `unexpected schema version: 7`.

- [ ] **Step 3: Create the migration**

`internal/store/migrations/000008_mcp_servers.up.sql`:
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

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store -run TestMigrate -v`
Expected: PASS (version 8, not dirty, idempotent).

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/000008_mcp_servers.up.sql internal/store/db_test.go
git commit -m "feat(store): add mcp_servers + bot_mcp_servers migration"
```

---

## Task 2: Domain entity + repository interface

**Files:**
- Modify: `internal/domain/entities.go`
- Modify: `internal/domain/repositories.go`

- [ ] **Step 1: Add the entity**

Append to `internal/domain/entities.go`:
```go
type MCPServer struct {
	ID         string
	Name       string
	ServerType string
	URL        string
	Command    string
	Args       []string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
```

- [ ] **Step 2: Add the repository interface**

Append to `internal/domain/repositories.go`:
```go
type MCPServerRepository interface {
	Create(ctx context.Context, server MCPServer) (MCPServer, error)
	GetByID(ctx context.Context, id string) (MCPServer, error)
	GetByName(ctx context.Context, name string) (MCPServer, error)
	List(ctx context.Context) ([]MCPServer, error)
	Update(ctx context.Context, server MCPServer) (MCPServer, error)
	DeleteByID(ctx context.Context, id string) error

	ListByBot(ctx context.Context, botID string) ([]MCPServer, error)
	ListEnabledByBot(ctx context.Context, botID string) ([]MCPServer, error)
	SetBotServers(ctx context.Context, botID string, serverIDs []string) error
	AttachToBot(ctx context.Context, botID, serverID string) error
	DetachFromBot(ctx context.Context, botID, serverID string) error
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/domain/...`
Expected: builds clean (no impl yet — that's Task 3).

- [ ] **Step 4: Commit**

```bash
git add internal/domain/entities.go internal/domain/repositories.go
git commit -m "feat(domain): add MCPServer entity and repository interface"
```

---

## Task 3: Store models + catalog CRUD

**Files:**
- Create: `internal/store/models/mcp_server.go`
- Create: `internal/store/repositories/mcp_server_repository.go`
- Create: `internal/store/repositories/mcp_server_repository_test.go`

- [ ] **Step 1: Write failing catalog tests**

`internal/store/repositories/mcp_server_repository_test.go`:
```go
package repositories_test

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/repositories"
	"github.com/benenen/myclaw/internal/testutil"
)

func newMCPRepo(t *testing.T) *repositories.MCPServerRepository {
	t.Helper()
	db := testutil.NewTestDB(t)
	return repositories.NewMCPServerRepository(db)
}

func TestMCPServerCRUD(t *testing.T) {
	repo := newMCPRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.MCPServer{
		ID: "mcp_1", Name: "fs", ServerType: "stdio",
		Command: "npx", Args: []string{"-y", "server"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "fs" || len(created.Args) != 2 {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := repo.GetByName(ctx, "fs")
	if err != nil || got.ID != "mcp_1" {
		t.Fatalf("getByName: %+v err=%v", got, err)
	}

	got.Enabled = false
	if _, err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, _ := repo.GetByID(ctx, "mcp_1")
	if again.Enabled {
		t.Fatalf("expected disabled after update")
	}

	if err := repo.DeleteByID(ctx, "mcp_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, "mcp_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
```

> Note: if `testutil` exposes a differently-named in-memory DB helper, use that — check `internal/testutil` and existing `*_repository_test.go` files for the exact constructor (e.g. `testutil.NewTestDB` / `testutil.OpenInMemory`).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/repositories -run TestMCPServerCRUD -v`
Expected: FAIL — `repositories.NewMCPServerRepository` undefined.

- [ ] **Step 3: Add the GORM models**

`internal/store/models/mcp_server.go`:
```go
package models

import "time"

type MCPServer struct {
	ID         string `gorm:"primaryKey"`
	Name       string `gorm:"not null;uniqueIndex"`
	ServerType string `gorm:"column:server_type;not null;default:'http'"`
	URL        string `gorm:"column:url;not null;default:''"`
	Command    string `gorm:"column:command;not null;default:''"`
	ArgsJSON   string `gorm:"column:args_json;not null;default:'[]'"`
	Enabled    *bool  `gorm:"not null;default:true"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (MCPServer) TableName() string { return "mcp_servers" }

type BotMCPServer struct {
	BotID       string `gorm:"column:bot_id;primaryKey"`
	MCPServerID string `gorm:"column:mcp_server_id;primaryKey"`
	CreatedAt   time.Time
}

func (BotMCPServer) TableName() string { return "bot_mcp_servers" }
```

- [ ] **Step 4: Add the catalog repository**

`internal/store/repositories/mcp_server_repository.go`:
```go
package repositories

import (
	"context"
	"encoding/json"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MCPServerRepository struct {
	db *gorm.DB
}

func NewMCPServerRepository(db *gorm.DB) *MCPServerRepository {
	return &MCPServerRepository{db: db}
}

func (r *MCPServerRepository) Create(ctx context.Context, s domain.MCPServer) (domain.MCPServer, error) {
	now := time.Now().UTC()
	m := toModelMCPServer(s)
	m.CreatedAt = now
	m.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return domain.MCPServer{}, err
	}
	return toDomainMCPServer(m), nil
}

func (r *MCPServerRepository) GetByID(ctx context.Context, id string) (domain.MCPServer, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *MCPServerRepository) GetByName(ctx context.Context, name string) (domain.MCPServer, error) {
	return r.first(ctx, "name = ?", name)
}

func (r *MCPServerRepository) first(ctx context.Context, query string, arg any) (domain.MCPServer, error) {
	var m models.MCPServer
	if err := r.db.WithContext(ctx).Where(query, arg).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.MCPServer{}, domain.ErrNotFound
		}
		return domain.MCPServer{}, err
	}
	return toDomainMCPServer(m), nil
}

func (r *MCPServerRepository) List(ctx context.Context) ([]domain.MCPServer, error) {
	var rows []models.MCPServer
	if err := r.db.WithContext(ctx).Order("name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDomainMCPServers(rows), nil
}

func (r *MCPServerRepository) Update(ctx context.Context, s domain.MCPServer) (domain.MCPServer, error) {
	m := toModelMCPServer(s)
	m.UpdatedAt = time.Now().UTC()
	if err := r.db.WithContext(ctx).Model(&models.MCPServer{}).Where("id = ?", s.ID).Updates(map[string]any{
		"name":        m.Name,
		"server_type": m.ServerType,
		"url":         m.URL,
		"command":     m.Command,
		"args_json":   m.ArgsJSON,
		"enabled":     m.Enabled,
		"updated_at":  m.UpdatedAt,
	}).Error; err != nil {
		return domain.MCPServer{}, err
	}
	return r.GetByID(ctx, s.ID)
}

func (r *MCPServerRepository) DeleteByID(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("mcp_server_id = ?", id).Delete(&models.BotMCPServer{}).Error; err != nil {
			return err
		}
		result := tx.Where("id = ?", id).Delete(&models.MCPServer{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

func toModelMCPServer(s domain.MCPServer) models.MCPServer {
	argsJSON := "[]"
	if len(s.Args) > 0 {
		data, _ := json.Marshal(s.Args)
		argsJSON = string(data)
	}
	enabled := s.Enabled
	return models.MCPServer{
		ID:         s.ID,
		Name:       s.Name,
		ServerType: s.ServerType,
		URL:        s.URL,
		Command:    s.Command,
		ArgsJSON:   argsJSON,
		Enabled:    &enabled,
	}
}

func toDomainMCPServer(m models.MCPServer) domain.MCPServer {
	var args []string
	if m.ArgsJSON != "" && m.ArgsJSON != "[]" {
		_ = json.Unmarshal([]byte(m.ArgsJSON), &args)
	}
	if args == nil {
		args = []string{}
	}
	enabled := true
	if m.Enabled != nil {
		enabled = *m.Enabled
	}
	return domain.MCPServer{
		ID:         m.ID,
		Name:       m.Name,
		ServerType: m.ServerType,
		URL:        m.URL,
		Command:    m.Command,
		Args:       args,
		Enabled:    enabled,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

func toDomainMCPServers(rows []models.MCPServer) []domain.MCPServer {
	items := make([]domain.MCPServer, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDomainMCPServer(row))
	}
	return items
}

var _ = clause.OnConflict{} // used by join methods in the next task
```

> `errors` import: add `"errors"` to the import block (used by `first`).

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/store/repositories -run TestMCPServerCRUD -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/models/mcp_server.go internal/store/repositories/mcp_server_repository.go internal/store/repositories/mcp_server_repository_test.go
git commit -m "feat(store): mcp server catalog repository"
```

---

## Task 4: Join methods + cascade

**Files:**
- Modify: `internal/store/repositories/mcp_server_repository.go`
- Modify: `internal/store/repositories/mcp_server_repository_test.go`

- [ ] **Step 1: Write failing join tests**

Append to `mcp_server_repository_test.go`:
```go
func TestMCPServerBotAttachment(t *testing.T) {
	repo := newMCPRepo(t)
	ctx := context.Background()

	a, _ := repo.Create(ctx, domain.MCPServer{ID: "mcp_a", Name: "a", ServerType: "http", URL: "http://a", Enabled: true})
	b, _ := repo.Create(ctx, domain.MCPServer{ID: "mcp_b", Name: "b", ServerType: "http", URL: "http://b", Enabled: false})

	// attach is idempotent
	if err := repo.AttachToBot(ctx, "bot_1", a.ID); err != nil {
		t.Fatalf("attach a: %v", err)
	}
	if err := repo.AttachToBot(ctx, "bot_1", a.ID); err != nil {
		t.Fatalf("re-attach a: %v", err)
	}
	if err := repo.AttachToBot(ctx, "bot_1", b.ID); err != nil {
		t.Fatalf("attach b: %v", err)
	}

	all, _ := repo.ListByBot(ctx, "bot_1")
	if len(all) != 2 {
		t.Fatalf("ListByBot = %d, want 2", len(all))
	}
	enabled, _ := repo.ListEnabledByBot(ctx, "bot_1")
	if len(enabled) != 1 || enabled[0].ID != "mcp_a" {
		t.Fatalf("ListEnabledByBot = %+v, want only mcp_a", enabled)
	}

	// replace-set
	if err := repo.SetBotServers(ctx, "bot_1", []string{b.ID}); err != nil {
		t.Fatalf("set: %v", err)
	}
	all, _ = repo.ListByBot(ctx, "bot_1")
	if len(all) != 1 || all[0].ID != "mcp_b" {
		t.Fatalf("after set ListByBot = %+v, want only mcp_b", all)
	}

	// detach
	if err := repo.DetachFromBot(ctx, "bot_1", b.ID); err != nil {
		t.Fatalf("detach: %v", err)
	}
	all, _ = repo.ListByBot(ctx, "bot_1")
	if len(all) != 0 {
		t.Fatalf("after detach = %d, want 0", len(all))
	}

	// delete cascades join rows
	repo.AttachToBot(ctx, "bot_2", a.ID)
	if err := repo.DeleteByID(ctx, a.ID); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	leftover, _ := repo.ListByBot(ctx, "bot_2")
	if len(leftover) != 0 {
		t.Fatalf("delete did not cascade: %+v", leftover)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/repositories -run TestMCPServerBotAttachment -v`
Expected: FAIL — `AttachToBot` undefined.

- [ ] **Step 3: Add the join methods**

Replace the trailing `var _ = clause.OnConflict{}` line in `mcp_server_repository.go` with:
```go
func (r *MCPServerRepository) ListByBot(ctx context.Context, botID string) ([]domain.MCPServer, error) {
	return r.listJoined(ctx, botID, false)
}

func (r *MCPServerRepository) ListEnabledByBot(ctx context.Context, botID string) ([]domain.MCPServer, error) {
	return r.listJoined(ctx, botID, true)
}

func (r *MCPServerRepository) listJoined(ctx context.Context, botID string, onlyEnabled bool) ([]domain.MCPServer, error) {
	q := r.db.WithContext(ctx).
		Joins("JOIN bot_mcp_servers bms ON bms.mcp_server_id = mcp_servers.id").
		Where("bms.bot_id = ?", botID)
	if onlyEnabled {
		q = q.Where("mcp_servers.enabled = ?", true)
	}
	var rows []models.MCPServer
	if err := q.Order("mcp_servers.name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDomainMCPServers(rows), nil
}

func (r *MCPServerRepository) AttachToBot(ctx context.Context, botID, serverID string) error {
	m := models.BotMCPServer{BotID: botID, MCPServerID: serverID, CreatedAt: time.Now().UTC()}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&m).Error
}

func (r *MCPServerRepository) DetachFromBot(ctx context.Context, botID, serverID string) error {
	return r.db.WithContext(ctx).
		Where("bot_id = ? AND mcp_server_id = ?", botID, serverID).
		Delete(&models.BotMCPServer{}).Error
}

func (r *MCPServerRepository) SetBotServers(ctx context.Context, botID string, serverIDs []string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bot_id = ?", botID).Delete(&models.BotMCPServer{}).Error; err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, id := range serverIDs {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if err := tx.Create(&models.BotMCPServer{BotID: botID, MCPServerID: id, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var _ domain.MCPServerRepository = (*MCPServerRepository)(nil)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/store/repositories -run TestMCPServer -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/store/repositories/mcp_server_repository.go internal/store/repositories/mcp_server_repository_test.go
git commit -m "feat(store): per-bot mcp attachment join + cascade"
```

---

## Task 5: App service

**Files:**
- Create: `internal/app/mcpserver/service.go`
- Create: `internal/app/mcpserver/service_test.go`

- [ ] **Step 1: Write failing service tests**

`internal/app/mcpserver/service_test.go`:
```go
package mcpserver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/app/mcpserver"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/repositories"
	"github.com/benenen/myclaw/internal/testutil"
)

func newSvc(t *testing.T) *mcpserver.Service {
	t.Helper()
	db := testutil.NewTestDB(t)
	return mcpserver.NewService(
		repositories.NewMCPServerRepository(db),
		repositories.NewBotRepository(db),
	)
}

func TestServiceCreateValidation(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, mcpserver.CreateInput{Name: "", ServerType: "http", URL: "x"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("empty name should be invalid, got %v", err)
	}
	if _, err := svc.Create(ctx, mcpserver.CreateInput{Name: "h", ServerType: "http"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("http without url should be invalid, got %v", err)
	}
	if _, err := svc.Create(ctx, mcpserver.CreateInput{Name: "s", ServerType: "stdio"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("stdio without command should be invalid, got %v", err)
	}
	if _, err := svc.Create(ctx, mcpserver.CreateInput{Name: "ok", ServerType: "http", URL: "http://x"}); err != nil {
		t.Fatalf("valid create failed: %v", err)
	}
	// duplicate name -> friendly invalid-arg, not raw driver error
	if _, err := svc.Create(ctx, mcpserver.CreateInput{Name: "ok", ServerType: "http", URL: "http://y"}); !errors.Is(err, domain.ErrInvalidArg) {
		t.Fatalf("duplicate name should be ErrInvalidArg, got %v", err)
	}
}

func TestServiceAttachValidatesExistence(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, mcpserver.CreateInput{Name: "srv", ServerType: "http", URL: "http://x"}); err != nil {
		t.Fatal(err)
	}
	// unknown bot
	if err := svc.AttachToBot(ctx, "nope", "srv"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("attach to unknown bot should be ErrNotFound, got %v", err)
	}
}
```

> If `testutil.NewTestDB` is not the actual helper name, mirror what `internal/app/bot/*_test.go` uses to obtain an in-memory DB + a seeded bot. To exercise the happy-path attach you may need to create a bot via `repositories.NewBotRepository(db).Create(...)`; check the bot repo signature first.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/mcpserver -v`
Expected: FAIL — package/`NewService` undefined.

- [ ] **Step 3: Implement the service**

`internal/app/mcpserver/service.go`:
```go
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/domain"
)

const (
	TypeHTTP  = "http"
	TypeStdio = "stdio"
)

type Service struct {
	repo domain.MCPServerRepository
	bots domain.BotRepository
}

func NewService(repo domain.MCPServerRepository, bots domain.BotRepository) *Service {
	return &Service{repo: repo, bots: bots}
}

type CreateInput struct {
	Name       string
	ServerType string
	URL        string
	Command    string
	Args       []string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (domain.MCPServer, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.MCPServer{}, fmt.Errorf("%w: name is required", domain.ErrInvalidArg)
	}
	serverType := strings.TrimSpace(in.ServerType)
	if serverType == "" {
		serverType = TypeHTTP
	}
	if serverType != TypeHTTP && serverType != TypeStdio {
		return domain.MCPServer{}, fmt.Errorf("%w: server_type must be http or stdio", domain.ErrInvalidArg)
	}
	if serverType == TypeHTTP && strings.TrimSpace(in.URL) == "" {
		return domain.MCPServer{}, fmt.Errorf("%w: url is required for http servers", domain.ErrInvalidArg)
	}
	if serverType == TypeStdio && strings.TrimSpace(in.Command) == "" {
		return domain.MCPServer{}, fmt.Errorf("%w: command is required for stdio servers", domain.ErrInvalidArg)
	}
	if _, err := s.repo.GetByName(ctx, name); err == nil {
		return domain.MCPServer{}, fmt.Errorf("%w: server %q already exists", domain.ErrInvalidArg, name)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.MCPServer{}, err
	}

	server := domain.MCPServer{
		ID:         domain.NewPrefixedID("mcp"),
		Name:       name,
		ServerType: serverType,
		URL:        strings.TrimSpace(in.URL),
		Command:    strings.TrimSpace(in.Command),
		Args:       append([]string(nil), in.Args...),
		Enabled:    true,
	}
	return s.repo.Create(ctx, server)
}

func (s *Service) List(ctx context.Context) ([]domain.MCPServer, error) {
	return s.repo.List(ctx)
}

func (s *Service) Remove(ctx context.Context, name string) error {
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	return s.repo.DeleteByID(ctx, server.ID) // cascades join rows
}

func (s *Service) SetEnabled(ctx context.Context, name string, enabled bool) (domain.MCPServer, error) {
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return domain.MCPServer{}, err
	}
	server.Enabled = enabled
	return s.repo.Update(ctx, server)
}

func (s *Service) ListByBot(ctx context.Context, botID string) ([]domain.MCPServer, error) {
	return s.repo.ListByBot(ctx, botID)
}

func (s *Service) AttachToBot(ctx context.Context, botID, serverName string) error {
	if err := s.requireBot(ctx, botID); err != nil {
		return err
	}
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(serverName))
	if err != nil {
		return err
	}
	return s.repo.AttachToBot(ctx, botID, server.ID)
}

func (s *Service) DetachFromBot(ctx context.Context, botID, serverName string) error {
	if err := s.requireBot(ctx, botID); err != nil {
		return err
	}
	server, err := s.repo.GetByName(ctx, strings.TrimSpace(serverName))
	if err != nil {
		return err
	}
	return s.repo.DetachFromBot(ctx, botID, server.ID)
}

func (s *Service) SetBotServers(ctx context.Context, botID string, serverIDs []string) error {
	for _, id := range serverIDs {
		if _, err := s.repo.GetByID(ctx, id); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("%w: mcp server %q not found", domain.ErrInvalidArg, id)
			}
			return err
		}
	}
	return s.repo.SetBotServers(ctx, botID, serverIDs)
}

func (s *Service) requireBot(ctx context.Context, botID string) error {
	if _, err := s.bots.GetByID(ctx, botID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: bot %q not found", domain.ErrNotFound, botID)
		}
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/app/mcpserver -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/mcpserver/
git commit -m "feat(mcpserver): service with validation and per-bot attach/detach"
```

---

## Task 6: Resolver injection

**Files:**
- Modify: `internal/app/bot/cli_resolver.go`
- Modify: `internal/app/bot/cli_resolver_test.go`

- [ ] **Step 1: Write the failing resolver test**

Add to `internal/app/bot/cli_resolver_test.go` (mirror the existing fake-repo style in that file; the resolver already has a `mcpServers domain.MCPServerRepository` field set via `SetMCPServerRepository`). Use a stub implementing `ListEnabledByBot`:
```go
func TestResolveInjectsAttachedEnabledMCPServers(t *testing.T) {
	// Arrange a resolver for an orchestrator bot with mcpURL set, and a stub
	// MCPServerRepository whose ListEnabledByBot returns one http server.
	// (Reuse the existing test scaffolding in this file for bots/capabilities.)
	stub := &stubMCPRepo{enabledByBot: map[string][]domain.MCPServer{
		"bot_orch": {{ID: "mcp_a", Name: "extra", ServerType: "http", URL: "http://extra", Enabled: true}},
	}}
	r := newResolverForOrchestratorBot(t, "bot_orch") // helper built from existing patterns
	r.SetMCPServerRepository(stub)

	spec, err := r.Resolve(context.Background(), "bot_orch")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg := mcpConfigArg(t, spec.Args) // helper: find value after "--mcp-config"
	if !strings.Contains(cfg, `"myclaw"`) || !strings.Contains(cfg, `"extra"`) {
		t.Fatalf("expected myclaw + extra in config, got %s", cfg)
	}
}
```
And the stub:
```go
type stubMCPRepo struct {
	domain.MCPServerRepository // embed; only ListEnabledByBot is exercised
	enabledByBot map[string][]domain.MCPServer
}

func (s *stubMCPRepo) ListEnabledByBot(_ context.Context, botID string) ([]domain.MCPServer, error) {
	return s.enabledByBot[botID], nil
}
```

> Implementation note for the worker: this file already constructs resolvers and orchestrator bots in other tests — reuse those exact helpers rather than inventing `newResolverForOrchestratorBot`/`mcpConfigArg` if equivalents exist. Embedding the interface in the stub means only `ListEnabledByBot` must be implemented.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/bot -run TestResolveInjectsAttachedEnabledMCPServers -v`
Expected: FAIL — config contains `myclaw` only (current code) or compile error for new helpers.

- [ ] **Step 3: Update `buildMCPConfigJSON` to take a botID**

In `internal/app/bot/cli_resolver.go`, change the call site (inside the `bot.Role == domain.BotRoleOrchestrator` block):
```go
		if r.mcpURL != "" {
			extra = append(extra, "--mcp-config", r.buildMCPConfigJSON(ctx, botID))
		}
```
and replace the function:
```go
func (r *BotCLIResolver) buildMCPConfigJSON(ctx context.Context, botID string) string {
	mcpServers := map[string]any{
		"myclaw": map[string]any{
			"type": "http",
			"url":  r.mcpURL,
		},
	}

	if r.mcpServers != nil {
		servers, err := r.mcpServers.ListEnabledByBot(ctx, botID)
		if err != nil {
			log.Printf("mcp server list failed for bot %s, using only myclaw: %v", botID, err)
		} else {
			for _, srv := range servers {
				cfg := map[string]any{"type": srv.ServerType}
				if srv.ServerType == "http" {
					cfg["url"] = srv.URL
				} else {
					args := srv.Args
					if args == nil {
						args = []string{}
					}
					cfg["command"] = srv.Command
					cfg["args"] = args
				}
				mcpServers[srv.Name] = cfg
			}
		}
	}

	data, _ := json.Marshal(map[string]any{"mcpServers": mcpServers})
	return string(data)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/app/bot -run TestResolve -v`
Expected: PASS (new test + existing resolver tests).

- [ ] **Step 5: Commit**

```bash
git add internal/app/bot/cli_resolver.go internal/app/bot/cli_resolver_test.go
git commit -m "feat(bot): inject per-bot enabled mcp servers into --mcp-config"
```

---

## Task 7: BotService + HTTP API

**Files:**
- Modify: `internal/app/bot/service.go`
- Modify: `internal/api/http/dto/bots.go`
- Modify: `internal/api/http/handlers/bots.go`
- Modify: `internal/api/http/handlers/router.go`
- Modify: `internal/api/http/handlers/bots_test.go`

- [ ] **Step 1: Write the failing handler test**

In `internal/api/http/handlers/bots_test.go`, add a test that POSTs `ConfigureBotAgent` with `mcp_server_ids` and asserts the response echoes them, plus that `GET /api/v1/mcp-servers` lists the catalog. Mirror the existing handler-test setup in that file (it already constructs a `BotService` + router). Key assertions:
```go
// after configuring a bot with mcp_server_ids: []string{"mcp_a"}
if !contains(resp.Data.MCPServerIDs, "mcp_a") {
	t.Fatalf("expected mcp_a echoed, got %+v", resp.Data.MCPServerIDs)
}
```

> The worker must adapt to the existing test harness in `bots_test.go` (how it builds the service and seeds a bot/capability). Construct `NewBotService(...)` with the new `mcpServers` repo argument (next step), seed one catalog server via `repositories.NewMCPServerRepository(db).Create(...)`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/http/handlers -run TestConfigureBotAgent -v`
Expected: FAIL — `MCPServerIDs` field / arg mismatch.

- [ ] **Step 3: Extend the DTOs**

In `internal/api/http/dto/bots.go`:
- Add to `BotResponse`: `MCPServerIDs []string `json:"mcp_server_ids"``
- Add to `ConfigureBotAgentRequest`: `MCPServerIDs []string `json:"mcp_server_ids,omitempty"``
- Add a new type:
```go
type MCPServerResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ServerType string `json:"server_type"`
	Enabled    bool   `json:"enabled"`
}
```

- [ ] **Step 4: Wire BotService**

In `internal/app/bot/service.go`:
- Add field `mcpServers domain.MCPServerRepository` to `BotService` and a parameter to `NewBotService` (append it last); assign it.
- Add `MCPServerIDs []string` to `ConfigureBotAgentInput` and to `BotListItem`.
- In `ConfigureBotAgent`, after the bot is updated, persist + read back:
```go
	if err := s.mcpServers.SetBotServers(ctx, bot.ID, input.MCPServerIDs); err != nil {
		return BotListItem{}, err
	}
	ids, err := s.attachedServerIDs(ctx, bot.ID)
	if err != nil {
		return BotListItem{}, err
	}
	// include ids in the returned BotListItem (set item.MCPServerIDs = ids)
```
- In `ListBots`, set each item's `MCPServerIDs` via `attachedServerIDs`.
- Add helpers:
```go
func (s *BotService) attachedServerIDs(ctx context.Context, botID string) ([]string, error) {
	servers, err := s.mcpServers.ListByBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(servers))
	for _, sv := range servers {
		ids = append(ids, sv.ID)
	}
	return ids, nil
}

func (s *BotService) ListMCPServers(ctx context.Context) ([]domain.MCPServer, error) {
	return s.mcpServers.List(ctx)
}
```

- [ ] **Step 5: Wire the handlers + route**

In `internal/api/http/handlers/bots.go`:
- In `ConfigureBotAgent`, pass `MCPServerIDs: req.MCPServerIDs` into the `ConfigureBotAgentInput`, and set `MCPServerIDs: result.MCPServerIDs` on the response.
- Add a handler:
```go
func ListMCPServers(svc *botapp.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		servers, err := svc.ListMCPServers(r.Context())
		if err != nil {
			httpapi.WriteErrorFromRequest(w, r, err) // match how other handlers report errors
			return
		}
		out := make([]dto.MCPServerResponse, 0, len(servers))
		for _, s := range servers {
			out = append(out, dto.MCPServerResponse{ID: s.ID, Name: s.Name, ServerType: s.ServerType, Enabled: s.Enabled})
		}
		httpapi.WriteOKFromRequest(w, r, out)
	}
}
```
> Match the exact error-writer used by sibling handlers in this file (e.g. how `ListAgentCapabilities`/`ConfigureBotAgent` report failures).

In `internal/api/http/handlers/router.go`, next to the agent-capabilities route:
```go
	mux.Handle("GET /api/v1/mcp-servers", wrap(ListMCPServers(deps.BotService)))
```

- [ ] **Step 6: Run to verify it passes**

Run: `go test ./internal/api/http/handlers ./internal/app/bot -v`
Expected: PASS. Then `go build ./...` to catch the changed `NewBotService` signature (fixed in Task 9).

- [ ] **Step 7: Commit**

```bash
git add internal/app/bot/service.go internal/api/http/dto/bots.go internal/api/http/handlers/bots.go internal/api/http/handlers/router.go internal/api/http/handlers/bots_test.go
git commit -m "feat(api): per-bot mcp_server_ids on bot config + mcp-servers list endpoint"
```

---

## Task 8: CLI command tree

**Files:**
- Create: `cmd/mcp.go`

- [ ] **Step 1: Implement the command**

`cmd/mcp.go` — `list`/`add`/`remove`/`enable`/`disable`/`attach`/`detach`, with `newMCPService` opening the DB and building the service with a bot repo. (Reproduce the reverted command shape, plus the two new subcommands and the `list --bot` flag; do NOT re-add the `// ensure context is imported` comment.) Core pieces:
```go
func newMCPService(stderr io.Writer) (*mcpserver.Service, func(), error) {
	// LoadDataPaths, ensure dir, store.Open, store.Migrate (as in the reverted version)
	repo := repositories.NewMCPServerRepository(db)
	botRepo := repositories.NewBotRepository(db)
	svc := mcpserver.NewService(repo, botRepo)
	// cleanup closes db
	return svc, cleanup, nil
}

func newMCPAttachCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach --bot <botID> --server <name>",
		Short: "Attach an MCP server to a bot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			botID, _ := cmd.Flags().GetString("bot")
			server, _ := cmd.Flags().GetString("server")
			if strings.TrimSpace(botID) == "" || strings.TrimSpace(server) == "" {
				return fmt.Errorf("--bot and --server are required")
			}
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := svc.AttachToBot(cmd.Context(), botID, server); err != nil {
				return fmt.Errorf("attach: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "attached %q to bot %q\n", server, botID)
			return nil
		},
	}
	cmd.Flags().String("bot", "", "bot id (required)")
	cmd.Flags().String("server", "", "mcp server name (required)")
	return cmd
}
// detach: identical shape calling svc.DetachFromBot.
// list --bot <id>: add a "bot" string flag to the list command; when set,
//   print svc.ListByBot(ctx, botID) instead of the full catalog.
```
Register all subcommands in `NewMCPCommand`.

- [ ] **Step 2: Verify it builds**

Run: `go build ./cmd/... .`
Expected: builds (the `mcp` command is registered in main.go in Task 9).

- [ ] **Step 3: Commit**

```bash
git add cmd/mcp.go
git commit -m "feat(cmd): myclaw mcp command with attach/detach"
```

---

## Task 9: Bootstrap + main wiring

**Files:**
- Modify: `internal/bootstrap/bootstrap.go`
- Modify: `main.go`

- [ ] **Step 1: Wire the repo**

In `internal/bootstrap/bootstrap.go`:
- After the other repos: `mcpServerRepo := repositories.NewMCPServerRepository(db)`.
- Append `mcpServerRepo` to the `bot.NewBotService(...)` call (last arg).
- After the resolver is built: `resolver.SetMCPServerRepository(mcpServerRepo)`.

- [ ] **Step 2: Register the CLI command**

In `main.go`, in `newRootCommand`:
```go
	root.AddCommand(cmd.NewMCPCommand(stderr))
```
and add to `writeUsage`:
```go
	fmt.Fprintln(w, "  myclaw mcp <list|add|remove|enable|disable|attach|detach> [...]")
```

- [ ] **Step 3: Build + full test + boot smoke**

```bash
go build ./...
go test ./...
CHANNEL_MASTER_KEY=$(openssl rand -base64 32) timeout 5 ./$(go build -o /tmp/myclaw . && echo /tmp/myclaw) server
```
Expected: build clean, all tests green, server logs `web server listening` with no migration error.

- [ ] **Step 4: CLI smoke**

```bash
go run . mcp add --name fs --type stdio --command npx --args -y,@modelcontextprotocol/server-filesystem
go run . mcp list
# (attach needs a real bot id from the web UI / DB)
```
Expected: `created MCP server "fs" (type=stdio)`, then a list line for `fs`.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/bootstrap.go main.go
git commit -m "feat: wire mcp server repository into bootstrap and root command"
```

---

## Task 10: Frontend attachment multi-select

**Files:**
- Modify: `internal/api/http/web/static/index.html`
- Modify: `internal/api/http/web/static/app.js`

- [ ] **Step 1: Add the field to the agent-config card**

In `index.html`, inside the bot-detail `// AGENT` card (next to the CLI-alias field), add:
```html
<div class="form-grid wide">
  <label for="detail-agent-mcp">MCP servers</label>
  <select id="detail-agent-mcp" multiple size="4"></select>
</div>
```

- [ ] **Step 2: Load the catalog and pre-select**

In `app.js`:
- Add a module-level `let mcpServers = [];` and, where other lookups load (near `agentCapabilities`), fetch once:
```js
async function loadMCPServers() {
  const res = await fetch('/api/v1/mcp-servers');
  const body = await res.json();
  mcpServers = body.data || [];
}
```
- In the bot-detail render (where `detail-agent-alias` is populated), render options and select the bot's attached ids:
```js
function renderMCPOptions(selectedIds) {
  const sel = document.getElementById('detail-agent-mcp');
  if (!sel) return;
  const chosen = new Set(selectedIds || []);
  sel.innerHTML = mcpServers.map(s =>
    `<option value="${s.id}" ${chosen.has(s.id) ? 'selected' : ''}>${s.name}${s.enabled ? '' : ' (disabled)'}</option>`
  ).join('');
}
// call: renderMCPOptions(bot?.mcp_server_ids || []);
```

- [ ] **Step 3: Include ids in the save**

In `saveSelectedBotAgent`, gather selected ids and add to the POST body, then update local state:
```js
  const mcpServerIds = Array.from(document.getElementById('detail-agent-mcp').selectedOptions).map(o => o.value);
  // ... in the request body:
  mcp_server_ids: mcpServerIds,
  // ... after success:
  bot.mcp_server_ids = updated.mcp_server_ids || [];
```

- [ ] **Step 4: Manual verification**

```bash
CHANNEL_MASTER_KEY=$(openssl rand -base64 32) go run . server
```
Then in a browser at `http://localhost:8080`: add a server via CLI first, open a bot, confirm the MCP multi-select lists it, select it, Save, reload the bot, confirm it stays selected. (Optionally use the webapp-testing skill for an automated check.)

- [ ] **Step 5: Commit**

```bash
git add internal/api/http/web/static/index.html internal/api/http/web/static/app.js
git commit -m "feat(web): attach MCP servers to a bot in the agent-config card"
```

---

## Self-Review

**Spec coverage:**
- Catalog (entity/repo/model/migration/service/CLI) → Tasks 1–5, 8. ✓
- Join table + per-bot repo methods → Tasks 1, 4. ✓
- Resolver injects enabled+attached (orchestrator-only, kill-switch) → Task 6. ✓
- API `mcp_server_ids` + `GET /api/v1/mcp-servers` → Task 7. ✓
- Web multi-select → Task 10. ✓
- Cleanups: no `config.go` (never created), no leftover comment (Task 8 note), friendly dup-name (Task 5). ✓
- Bootstrap wiring → Task 9. ✓

**Placeholder scan:** Implementation steps carry real code. Tasks 7/8/10 contain deliberate "match the existing harness" notes rather than full reproductions of large existing files — the worker must read the sibling code; the new code is fully specified.

**Type consistency:** `MCPServerRepository` methods (Task 2) match impls (Tasks 3–4) and the resolver stub (Task 6). `mcpserver.NewService(repo, bots)` is consistent across Tasks 5, 8. `NewBotService(... , mcpServers)` consistent across Tasks 7, 9. DTO `mcp_server_ids` consistent across Tasks 7, 10.

**Open verification points for the worker:** exact `internal/testutil` DB helper name; the existing resolver/handler test scaffolding to reuse; the sibling error-writer (`WriteErrorFromRequest` or equivalent) in handlers.
