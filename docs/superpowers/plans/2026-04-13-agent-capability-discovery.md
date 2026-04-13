# Agent Capability Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build startup CLI capability discovery, persist a global machine capability inventory, and let each bot choose a capability plus execution mode for message handling.

**Architecture:** Add a new global `agent_capabilities` persistence layer and startup scanner, extend bot storage with selected capability and mode, expose HTTP/UI configuration, and replace the single bootstrap-time `agent.Spec` with per-bot resolution at runtime. Keep execution on the existing agent manager/driver stack so the feature changes selection and routing, not process execution semantics.

**Tech Stack:** Go 1.23, net/http, GORM with SQLite, embedded SQL migrations, vanilla HTML/CSS/JS admin UI, existing agent manager/oneshot driver.

---

## File Structure

### New files

- `internal/store/models/agent_capability.go` — GORM model for persisted machine-wide agent capabilities.
- `internal/store/repositories/agent_capability_repository.go` — repository methods for upsert, get, and list operations on capabilities.
- `internal/store/repositories/agent_capability_repository_test.go` — repository coverage for inventory persistence.
- `internal/app/agent_capability_scanner.go` — startup whitelist scan using `exec.LookPath` and repository upserts.
- `internal/app/agent_capability_scanner_test.go` — scanner coverage using injected lookups.
- `internal/app/bot_agent_resolver.go` — translate bot capability selection into `agent.Spec`.
- `internal/app/bot_agent_resolver_test.go` — resolver validation tests.
- `internal/api/http/dto/agent_capabilities.go` — DTOs for capability list and bot agent configuration.
- `internal/api/http/handlers/agent_capabilities.go` — list-capabilities and configure-bot-agent handlers.
- `internal/store/migrations/0002_agent_capabilities.sql` — schema changes for `agent_capabilities` and bot selection fields.

### Modified files

- `internal/store/db.go` — run both `0001_init.sql` and `0002_agent_capabilities.sql` in order.
- `internal/domain/entities.go` — add agent capability selection fields and capability entity.
- `internal/domain/repositories.go` — add capability repository contract.
- `internal/store/models/bot.go` — persist bot capability selection fields.
- `internal/store/repositories/bot_repository.go` — read/write bot capability selection.
- `internal/store/repositories/bot_repository_test.go` — cover new bot fields.
- `internal/app/bot_service.go` — create/list/configure bot capability settings.
- `internal/app/bot_service_test.go` — add service tests for capability configuration.
- `internal/app/bot_message_orchestrator.go` — resolve per-bot `agent.Spec` dynamically.
- `internal/app/bot_message_orchestrator_test.go` — verify per-bot capability routing and missing-config reply.
- `internal/bootstrap/bootstrap.go` — wire repository, scanner, resolver, handlers, and dynamic orchestrator dependencies.
- `internal/bootstrap/bootstrap_test.go` — verify bootstrap wiring still succeeds with scanner and repository additions.
- `internal/api/http/dto/bots.go` — expose bot capability selection in request/response payloads.
- `internal/api/http/handlers/bots.go` — accept capability selection on create, return selection on list/create/configure.
- `internal/api/http/handlers/bots_test.go` — API coverage for list/create/configure bot agent settings.
- `internal/api/http/handlers/router.go` — register new capability and configuration routes.
- `internal/api/http/web/index.html` — fetch capabilities, render selects, submit bot agent configuration, and show selected capability/mode.

---

### Task 1: Add schema and persistence for agent capabilities

**Files:**
- Create: `internal/store/models/agent_capability.go`
- Create: `internal/store/repositories/agent_capability_repository.go`
- Create: `internal/store/repositories/agent_capability_repository_test.go`
- Create: `internal/store/migrations/0002_agent_capabilities.sql`
- Modify: `internal/store/db.go:25-33`
- Modify: `internal/store/models/bot.go:5-16`
- Modify: `internal/store/repositories/bot_repository.go:20-118`
- Modify: `internal/store/repositories/bot_repository_test.go`
- Modify: `internal/domain/entities.go:13-54`
- Modify: `internal/domain/repositories.go:5-29`

- [ ] **Step 1: Write the failing repository tests**

```go
func TestAgentCapabilityRepositoryUpsertAndList(t *testing.T) {
	db := testDB(t)
	repo := NewAgentCapabilityRepository(db)
	ctx := context.Background()

	first, err := repo.Upsert(ctx, domain.AgentCapability{
		ID:             "cap_codex",
		Key:            "codex",
		Label:          "Codex CLI",
		Command:        "codex",
		Args:           []string{"reply"},
		SupportedModes: []string{"oneshot", "session"},
		Available:      true,
		DetectionSource:"path_scan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Key != "codex" || !first.Available {
		t.Fatalf("unexpected capability: %#v", first)
	}

	_, err = repo.Upsert(ctx, domain.AgentCapability{
		ID:             "cap_codex_new",
		Key:            "codex",
		Label:          "Codex CLI",
		Command:        "/usr/local/bin/codex",
		Args:           []string{"reply"},
		SupportedModes: []string{"oneshot", "session"},
		Available:      false,
		DetectionSource:"path_scan",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].Command != "/usr/local/bin/codex" || items[0].Available {
		t.Fatalf("unexpected stored capability: %#v", items[0])
	}
}

func TestBotRepositoryPersistsAgentSelection(t *testing.T) {
	db := testDB(t)
	repo := NewBotRepository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.Bot{
		ID:                "bot_1",
		UserID:            "usr_1",
		Name:              "demo",
		ChannelType:       "wechat",
		ConnectionStatus:  domain.BotConnectionStatusLoginRequired,
		AgentCapabilityID: "cap_codex",
		AgentMode:         "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AgentCapabilityID != "cap_codex" || created.AgentMode != "session" {
		t.Fatalf("unexpected created bot: %#v", created)
	}

	stored, err := repo.GetByID(ctx, "bot_1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.AgentCapabilityID != "cap_codex" || stored.AgentMode != "session" {
		t.Fatalf("unexpected stored bot: %#v", stored)
	}
}
```

- [ ] **Step 2: Run the focused repository tests and verify they fail**

Run: `go test ./internal/store/repositories -run 'TestAgentCapabilityRepositoryUpsertAndList|TestBotRepositoryPersistsAgentSelection'`
Expected: FAIL with undefined `domain.AgentCapability`, missing repository, and missing bot fields.

- [ ] **Step 3: Add the migration and domain/model definitions**

```sql
CREATE TABLE IF NOT EXISTS agent_capabilities (
    id TEXT PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    command TEXT NOT NULL,
    args_json TEXT NOT NULL DEFAULT '[]',
    supported_modes_json TEXT NOT NULL DEFAULT '[]',
    available INTEGER NOT NULL DEFAULT 0,
    detection_source TEXT NOT NULL DEFAULT '',
    last_detected_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

ALTER TABLE bots ADD COLUMN agent_capability_id TEXT NOT NULL DEFAULT '';
ALTER TABLE bots ADD COLUMN agent_mode TEXT NOT NULL DEFAULT '';
```

```go
type AgentCapability struct {
	ID              string
	Key             string
	Label           string
	Command         string
	Args            []string
	SupportedModes  []string
	Available       bool
	DetectionSource string
	LastDetectedAt  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Bot struct {
	ID                string
	UserID            string
	Name              string
	ChannelType       string
	ChannelAccountID  string
	ConnectionStatus  string
	ConnectionError   string
	AgentCapabilityID string
	AgentMode         string
	LastConnectedAt   *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
```

```go
func Migrate(db *gorm.DB) error {
	for _, name := range []string{"0001_init.sql", "0002_agent_capabilities.sql"} {
		sql, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := db.Exec(string(sql)).Error; err != nil {
			return fmt.Errorf("exec migration %s: %w", name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Implement the repository and bot persistence wiring**

```go
type AgentCapabilityRepository struct {
	db *gorm.DB
}

func NewAgentCapabilityRepository(db *gorm.DB) *AgentCapabilityRepository {
	return &AgentCapabilityRepository{db: db}
}

func (r *AgentCapabilityRepository) Upsert(ctx context.Context, capability domain.AgentCapability) (domain.AgentCapability, error) {
	now := time.Now().UTC()
	argsJSON, _ := json.Marshal(capability.Args)
	modesJSON, _ := json.Marshal(capability.SupportedModes)
	m := models.AgentCapability{
		ID:                 capability.ID,
		Key:                capability.Key,
		Label:              capability.Label,
		Command:            capability.Command,
		ArgsJSON:           string(argsJSON),
		SupportedModesJSON: string(modesJSON),
		Available:          capability.Available,
		DetectionSource:    capability.DetectionSource,
		LastDetectedAt:     capability.LastDetectedAt,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "command", "args_json", "supported_modes_json", "available", "detection_source", "last_detected_at", "updated_at"}),
	}).Create(&m).Error; err != nil {
		return domain.AgentCapability{}, err
	}
	return r.GetByKey(ctx, capability.Key)
}
```

```go
m := models.Bot{
	ID:                bot.ID,
	UserID:            bot.UserID,
	Name:              bot.Name,
	ChannelType:       bot.ChannelType,
	ChannelAccountID:  bot.ChannelAccountID,
	ConnectionStatus:  bot.ConnectionStatus,
	ConnectionError:   bot.ConnectionError,
	AgentCapabilityID: bot.AgentCapabilityID,
	AgentMode:         bot.AgentMode,
	LastConnectedAt:   bot.LastConnectedAt,
	CreatedAt:         now,
	UpdatedAt:         now,
}
```

- [ ] **Step 5: Run the focused repository tests and verify they pass**

Run: `go test ./internal/store/repositories -run 'TestAgentCapabilityRepositoryUpsertAndList|TestBotRepositoryPersistsAgentSelection'`
Expected: PASS

- [ ] **Step 6: Commit the persistence changes**

```bash
git add internal/domain/entities.go internal/domain/repositories.go internal/store/db.go internal/store/migrations/0002_agent_capabilities.sql internal/store/models/agent_capability.go internal/store/models/bot.go internal/store/repositories/agent_capability_repository.go internal/store/repositories/agent_capability_repository_test.go internal/store/repositories/bot_repository.go internal/store/repositories/bot_repository_test.go
git commit -m "feat: persist agent capabilities and bot agent settings"
```

### Task 2: Add startup capability scanning and per-bot resolution

**Files:**
- Create: `internal/app/agent_capability_scanner.go`
- Create: `internal/app/agent_capability_scanner_test.go`
- Create: `internal/app/bot_agent_resolver.go`
- Create: `internal/app/bot_agent_resolver_test.go`
- Modify: `internal/bootstrap/bootstrap.go:26-112`
- Modify: `internal/bootstrap/bootstrap_test.go:12-98`
- Modify: `internal/app/bot_message_orchestrator.go:13-257`
- Modify: `internal/app/bot_message_orchestrator_test.go`

- [ ] **Step 1: Write the failing scanner and resolver tests**

```go
func TestAgentCapabilityScannerMarksDetectedAndMissingCommands(t *testing.T) {
	repo := &capabilityRepoStub{}
	scanner := NewAgentCapabilityScanner(repo, func(name string) (string, error) {
		switch name {
		case "codex":
			return "/usr/local/bin/codex", nil
		default:
			return "", exec.ErrNotFound
		}
	})

	if err := scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.upserts) != 2 {
		t.Fatalf("len(upserts) = %d", len(repo.upserts))
	}
	if !repo.upserts[0].Available || repo.upserts[1].Available {
		t.Fatalf("unexpected scan results: %#v", repo.upserts)
	}
}

func TestBotAgentResolverReturnsConfiguredSpec(t *testing.T) {
	resolver := NewBotAgentResolver(
		&botRepoStub{bot: domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_codex", AgentMode: "session"}},
		&capRepoStub{capability: domain.AgentCapability{ID: "cap_codex", Command: "codex", SupportedModes: []string{"oneshot", "session"}, Available: true}},
	)

	spec, err := resolver.Resolve(context.Background(), "bot_1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "codex" || spec.Type != "session" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}
```

- [ ] **Step 2: Run the focused scanner/resolver tests and verify they fail**

Run: `go test ./internal/app -run 'TestAgentCapabilityScannerMarksDetectedAndMissingCommands|TestBotAgentResolverReturnsConfiguredSpec'`
Expected: FAIL with missing scanner and resolver symbols.

- [ ] **Step 3: Implement the scanner and resolver**

```go
var defaultCapabilitySeeds = []domain.AgentCapability{
	{ID: "cap_codex", Key: "codex", Label: "Codex CLI", Command: "codex", SupportedModes: []string{"oneshot", "session"}},
	{ID: "cap_claude", Key: "claude", Label: "Claude Code", Command: "claude", SupportedModes: []string{"oneshot", "session"}},
}

func (s *AgentCapabilityScanner) Scan(ctx context.Context) error {
	for _, seed := range defaultCapabilitySeeds {
		capability := seed
		path, err := s.lookPath(seed.Command)
		now := time.Now().UTC()
		if err == nil {
			capability.Command = path
			capability.Available = true
			capability.DetectionSource = "path_scan"
			capability.LastDetectedAt = &now
		} else {
			capability.Available = false
			capability.DetectionSource = "path_scan"
		}
		if _, upsertErr := s.repo.Upsert(ctx, capability); upsertErr != nil {
			return upsertErr
		}
	}
	return nil
}
```

```go
func (r *BotAgentResolver) Resolve(ctx context.Context, botID string) (agent.Spec, error) {
	bot, err := r.bots.GetByID(ctx, botID)
	if err != nil {
		return agent.Spec{}, err
	}
	if bot.AgentCapabilityID == "" {
		return agent.Spec{}, ErrBotAgentNotConfigured
	}
	capability, err := r.capabilities.GetByID(ctx, bot.AgentCapabilityID)
	if err != nil {
		return agent.Spec{}, err
	}
	if !capability.Available {
		return agent.Spec{}, ErrAgentCapabilityUnavailable
	}
	if !slices.Contains(capability.SupportedModes, bot.AgentMode) {
		return agent.Spec{}, ErrUnsupportedAgentMode
	}
	return agent.Spec{
		Type:    bot.AgentMode,
		Command: capability.Command,
		Args:    append([]string(nil), capability.Args...),
		Timeout: r.timeout,
	}, nil
}
```

- [ ] **Step 4: Refactor the orchestrator and bootstrap wiring to use resolver-based specs**

```go
type specResolver interface {
	Resolve(ctx context.Context, botID string) (agent.Spec, error)
}

type BotMessageOrchestrator struct {
	mu             sync.Mutex
	bots           map[string]*botState
	seen           map[string]seenMessageState
	agents         agentManager
	replies        replyGateway
	resolver       specResolver
	messageContext func(context.Context) context.Context
	workerIdleTime time.Duration
	processingTimeout time.Duration
	replyTimeout   time.Duration
}
```

```go
spec, err := o.resolver.Resolve(ctx, botID)
if err != nil {
	o.replyWithTimeout(ctx, msg, agent.Response{Text: failedReply})
	o.finishMessageEventually(msg, false)
	return
}
resp, err := o.agents.Send(ctx, botID, spec, agent.Request{
	BotID:     msg.BotID,
	UserID:    msg.From,
	MessageID: msg.MessageID,
	Prompt:    msg.Text,
})
```

```go
capabilityRepo := repositories.NewAgentCapabilityRepository(db)
scanner := app.NewAgentCapabilityScanner(capabilityRepo, exec.LookPath)
if err := scanner.Scan(context.Background()); err != nil {
	logger.Info("agent capability scan failed", "error", err)
}
resolver := app.NewBotAgentResolver(botRepo, capabilityRepo, cfg.AgentCLITimeout)
orchestrator := app.NewBotMessageOrchestrator(agentManager, replyGateway, resolver)
```

- [ ] **Step 5: Run the focused app/bootstrap tests and verify they pass**

Run: `go test ./internal/app ./internal/bootstrap -run 'TestAgentCapabilityScannerMarksDetectedAndMissingCommands|TestBotAgentResolverReturnsConfiguredSpec|TestOrchestratorUsesResolvedSpecPerBot|TestBootstrapBuildsDependencies'`
Expected: PASS

- [ ] **Step 6: Commit the scanner and resolver changes**

```bash
git add internal/app/agent_capability_scanner.go internal/app/agent_capability_scanner_test.go internal/app/bot_agent_resolver.go internal/app/bot_agent_resolver_test.go internal/app/bot_message_orchestrator.go internal/app/bot_message_orchestrator_test.go internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go
git commit -m "feat: resolve bot agent capabilities at runtime"
```

### Task 3: Add bot service support for capability selection

**Files:**
- Modify: `internal/app/bot_service.go:12-293`
- Modify: `internal/app/bot_service_test.go`
- Modify: `internal/api/http/dto/bots.go:3-52`

- [ ] **Step 1: Write the failing bot service tests**

```go
func TestCreateBotStoresAgentSelection(t *testing.T) {
	svc := newBotService(t)
	out, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID:     "user-1",
		Name:               "demo",
		ChannelType:        "wechat",
		AgentCapabilityID:  "cap_codex",
		AgentMode:          "oneshot",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.AgentCapabilityID != "cap_codex" || out.AgentMode != "oneshot" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestConfigureBotAgentUpdatesStoredBot(t *testing.T) {
	svc := newBotService(t)
	created := createBotForTest(t, svc)
	updated, err := svc.ConfigureBotAgent(context.Background(), ConfigureBotAgentInput{
		BotID:              created.BotID,
		AgentCapabilityID:  "cap_claude",
		AgentMode:          "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentCapabilityID != "cap_claude" || updated.AgentMode != "session" {
		t.Fatalf("unexpected update output: %#v", updated)
	}
}
```

- [ ] **Step 2: Run the focused bot service tests and verify they fail**

Run: `go test ./internal/app -run 'TestCreateBotStoresAgentSelection|TestConfigureBotAgentUpdatesStoredBot'`
Expected: FAIL with missing fields and missing `ConfigureBotAgent`.

- [ ] **Step 3: Extend service input/output and add configuration method**

```go
type CreateBotInput struct {
	ExternalUserID    string
	Name              string
	ChannelType       string
	AgentCapabilityID string
	AgentMode         string
}

type CreateBotOutput struct {
	BotID             string
	Name              string
	ChannelType       string
	ConnectionStatus  string
	ChannelAccountID  string
	AgentCapabilityID string
	AgentMode         string
}

type ConfigureBotAgentInput struct {
	BotID             string
	AgentCapabilityID string
	AgentMode         string
}
```

```go
func (s *BotService) ConfigureBotAgent(ctx context.Context, input ConfigureBotAgentInput) (BotListItem, error) {
	if input.BotID == "" || input.AgentCapabilityID == "" || input.AgentMode == "" {
		return BotListItem{}, domain.ErrInvalidArg
	}
	bot, err := s.bots.GetByID(ctx, input.BotID)
	if err != nil {
		return BotListItem{}, err
	}
	bot.AgentCapabilityID = input.AgentCapabilityID
	bot.AgentMode = input.AgentMode
	updated, err := s.bots.Update(ctx, bot)
	if err != nil {
		return BotListItem{}, err
	}
	return BotListItem{
		BotID:             updated.ID,
		Name:              updated.Name,
		ChannelType:       updated.ChannelType,
		ConnectionStatus:  updated.ConnectionStatus,
		ChannelAccountID:  updated.ChannelAccountID,
		AgentCapabilityID: updated.AgentCapabilityID,
		AgentMode:         updated.AgentMode,
	}, nil
}
```

- [ ] **Step 4: Expose the new fields in bot DTOs**

```go
type CreateBotRequest struct {
	UserID            string `json:"user_id"`
	Name              string `json:"name"`
	ChannelType       string `json:"channel_type"`
	AgentCapabilityID string `json:"agent_capability_id,omitempty"`
	AgentMode         string `json:"agent_mode,omitempty"`
}

type BotResponse struct {
	BotID             string `json:"bot_id"`
	Name              string `json:"name"`
	ChannelType       string `json:"channel_type"`
	ConnectionStatus  string `json:"connection_status"`
	ChannelAccountID  string `json:"channel_account_id,omitempty"`
	AgentCapabilityID string `json:"agent_capability_id,omitempty"`
	AgentMode         string `json:"agent_mode,omitempty"`
}
```

- [ ] **Step 5: Run the focused bot service tests and verify they pass**

Run: `go test ./internal/app -run 'TestCreateBotStoresAgentSelection|TestConfigureBotAgentUpdatesStoredBot'`
Expected: PASS

- [ ] **Step 6: Commit the bot service changes**

```bash
git add internal/app/bot_service.go internal/app/bot_service_test.go internal/api/http/dto/bots.go
git commit -m "feat: add bot agent capability settings"
```

### Task 4: Expose capability and bot configuration APIs

**Files:**
- Create: `internal/api/http/dto/agent_capabilities.go`
- Create: `internal/api/http/handlers/agent_capabilities.go`
- Modify: `internal/api/http/handlers/router.go:10-24`
- Modify: `internal/api/http/handlers/bots.go:14-184`
- Modify: `internal/api/http/handlers/bots_test.go`

- [ ] **Step 1: Write the failing HTTP handler tests**

```go
func TestListAgentCapabilities(t *testing.T) {
	h := ListAgentCapabilities(&stubCapabilityService{items: []app.AgentCapabilityListItem{{
		ID: "cap_codex", Key: "codex", Label: "Codex CLI", Available: true, SupportedModes: []string{"oneshot", "session"},
	}}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-capabilities", nil)
	w := httptest.NewRecorder()
	h(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"key":"codex"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestConfigureBotAgent(t *testing.T) {
	svc := newHTTPBotServiceStub()
	h := ConfigureBotAgent(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/agent", strings.NewReader(`{"bot_id":"bot_1","agent_capability_id":"cap_codex","agent_mode":"session"}`))
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run the focused HTTP tests and verify they fail**

Run: `go test ./internal/api/http/handlers -run 'TestListAgentCapabilities|TestConfigureBotAgent'`
Expected: FAIL with missing handlers and DTOs.

- [ ] **Step 3: Add DTOs and handlers for capability listing and bot agent configuration**

```go
type AgentCapabilityResponse struct {
	ID             string   `json:"id"`
	Key            string   `json:"key"`
	Label          string   `json:"label"`
	Available      bool     `json:"available"`
	SupportedModes []string `json:"supported_modes"`
}

type ConfigureBotAgentRequest struct {
	BotID             string `json:"bot_id"`
	AgentCapabilityID string `json:"agent_capability_id"`
	AgentMode         string `json:"agent_mode"`
}
```

```go
func ListAgentCapabilities(svc capabilityLister) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		items, err := svc.ListAgentCapabilities(r.Context())
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		resp := make([]dto.AgentCapabilityResponse, 0, len(items))
		for _, item := range items {
			resp = append(resp, dto.AgentCapabilityResponse{
				ID: item.ID, Key: item.Key, Label: item.Label, Available: item.Available, SupportedModes: item.SupportedModes,
			})
		}
		httpapi.WriteOKFromRequest(w, r, resp)
	}
}
```

```go
func ConfigureBotAgent(svc *app.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.ConfigureBotAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		result, err := svc.ConfigureBotAgent(r.Context(), app.ConfigureBotAgentInput{
			BotID: req.BotID, AgentCapabilityID: req.AgentCapabilityID, AgentMode: req.AgentMode,
		})
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		httpapi.WriteOKFromRequest(w, r, dto.BotResponse{
			BotID: result.BotID, Name: result.Name, ChannelType: result.ChannelType, ConnectionStatus: result.ConnectionStatus,
			ChannelAccountID: result.ChannelAccountID, AgentCapabilityID: result.AgentCapabilityID, AgentMode: result.AgentMode,
		})
	}
}
```

- [ ] **Step 4: Register the routes and include new fields on bot create/list responses**

```go
mux.Handle("GET /api/v1/agent-capabilities", wrap(ListAgentCapabilities(deps.BotService)))
mux.Handle("POST /api/v1/bots/agent", wrap(ConfigureBotAgent(deps.BotService)))
```

```go
result, err := svc.CreateBot(r.Context(), app.CreateBotInput{
	ExternalUserID:    req.UserID,
	Name:              req.Name,
	ChannelType:       req.ChannelType,
	AgentCapabilityID: req.AgentCapabilityID,
	AgentMode:         req.AgentMode,
})
```

- [ ] **Step 5: Run the focused HTTP tests and verify they pass**

Run: `go test ./internal/api/http/handlers -run 'TestListAgentCapabilities|TestConfigureBotAgent'`
Expected: PASS

- [ ] **Step 6: Commit the HTTP API changes**

```bash
git add internal/api/http/dto/agent_capabilities.go internal/api/http/handlers/agent_capabilities.go internal/api/http/handlers/bots.go internal/api/http/handlers/bots_test.go internal/api/http/handlers/router.go
git commit -m "feat: expose agent capability configuration APIs"
```

### Task 5: Update the admin UI for capability selection

**Files:**
- Modify: `internal/api/http/web/index.html`

- [ ] **Step 1: Write the failing web embed test for new UI strings**

```go
func TestWebEmbedIncludesAgentCapabilityControls(t *testing.T) {
	body := readEmbeddedIndexHTML(t)
	for _, needle := range []string{"create-bot-agent-capability", "create-bot-agent-mode", "loadAgentCapabilities", "configureSelectedBotAgent"} {
		if !strings.Contains(body, needle) {
			t.Fatalf("missing %q in embedded html", needle)
		}
	}
}
```

- [ ] **Step 2: Run the focused web test and verify it fails**

Run: `go test ./internal/api/http/web -run TestWebEmbedIncludesAgentCapabilityControls`
Expected: FAIL because the UI does not yet contain capability controls.

- [ ] **Step 3: Add capability loading, bot configuration controls, and dependent mode selects**

```html
<div class="field wide">
  <label for="create-bot-agent-capability">CLI Capability</label>
  <select id="create-bot-agent-capability"></select>
</div>
<div class="field">
  <label for="create-bot-agent-mode">Mode</label>
  <select id="create-bot-agent-mode"></select>
</div>
```

```js
let agentCapabilities = [];

async function loadAgentCapabilities() {
  const data = await api('GET', '/agent-capabilities');
  agentCapabilities = data.data || [];
  renderCreateBotCapabilityOptions();
}

function renderCreateBotCapabilityOptions() {
  const select = document.getElementById('create-bot-agent-capability');
  const available = agentCapabilities.filter(item => item.available);
  select.innerHTML = available.map(item => `<option value="${item.id}">${item.label}</option>`).join('');
  renderCreateBotModeOptions();
}

function renderCreateBotModeOptions() {
  const capability = agentCapabilities.find(item => item.id === document.getElementById('create-bot-agent-capability').value);
  const modes = capability?.supported_modes || [];
  document.getElementById('create-bot-agent-mode').innerHTML = modes.map(mode => `<option value="${mode}">${mode}</option>`).join('');
}
```

```js
async function configureSelectedBotAgent() {
  if (!selectedBotId) return;
  await api('POST', '/bots/agent', {
    bot_id: selectedBotId,
    agent_capability_id: document.getElementById('bot-agent-capability').value,
    agent_mode: document.getElementById('bot-agent-mode').value,
  });
  await loadBots(selectedBotId);
}
```

- [ ] **Step 4: Run the focused web test and verify it passes**

Run: `go test ./internal/api/http/web -run TestWebEmbedIncludesAgentCapabilityControls`
Expected: PASS

- [ ] **Step 5: Commit the UI changes**

```bash
git add internal/api/http/web/index.html
git commit -m "feat: add bot agent capability selection UI"
```

### Task 6: Run full verification and clean up plan gaps

**Files:**
- Modify: any files from Tasks 1-5 if verification exposes mismatches
- Test: `internal/store/repositories/agent_capability_repository_test.go`
- Test: `internal/app/agent_capability_scanner_test.go`
- Test: `internal/app/bot_agent_resolver_test.go`
- Test: `internal/api/http/handlers/bots_test.go`
- Test: `internal/api/http/web/embed_test.go`

- [ ] **Step 1: Run the targeted package tests**

Run: `go test ./internal/store/repositories ./internal/app ./internal/api/http/handlers ./internal/api/http/web ./internal/bootstrap`
Expected: PASS

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: If a mismatch appears, fix only the failing contract**

```go
if err := validateAgentSelection(input.AgentCapabilityID, input.AgentMode); err != nil {
	return BotListItem{}, domain.ErrInvalidArg
}
```

```go
if bot.AgentCapabilityID == "" {
	return agent.Spec{}, ErrBotAgentNotConfigured
}
```

Use the smallest fix that aligns persisted bot state, resolver expectations, HTTP payloads, and UI behavior.

- [ ] **Step 4: Re-run the failing test package and then the full suite**

Run: `go test ./path/to/failing/package && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit the verified final state**

```bash
git add internal/app internal/api/http internal/bootstrap internal/domain internal/store
git commit -m "feat: add startup agent capability discovery"
```

---

## Self-Review

### Spec coverage

| Spec requirement | Covered by |
|---|---|
| startup scan supported CLI commands | Task 2 |
| persist global capability list | Task 1 |
| per-bot capability selection | Tasks 1, 3, 4, 5 |
| per-bot execution mode selection | Tasks 1, 3, 4, 5 |
| runtime per-bot spec resolution | Task 2 |
| admin UI selection | Task 5 |
| non-blocking startup on scan failure | Task 2 |
| whitelist-only detection | Task 2 |

No uncovered spec sections remain.

### Placeholder scan

Checked for `TODO`, `TBD`, vague testing steps, and cross-task references without content. No unresolved placeholders remain.

### Type consistency

The plan uses these stable names consistently across tasks:

- `domain.AgentCapability`
- `AgentCapabilityRepository`
- `AgentCapabilityScanner`
- `BotAgentResolver`
- `ConfigureBotAgent`
- `AgentCapabilityID`
- `AgentMode`

The repository, service, HTTP, UI, and runtime tasks all use the same field and method names.
