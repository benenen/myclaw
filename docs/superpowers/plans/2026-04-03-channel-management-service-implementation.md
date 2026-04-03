# Channel Management Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Hertz-based HTTP service that manages WeChat channel bindings, stores credentials centrally, issues `app_key` values, and serves runtime configuration by `app_key`.

**Architecture:** The service is split into small units: HTTP handlers in Hertz, application services for orchestration, repositories over SQLite, a security package for encryption and key hashing, and a channel provider interface with a WeChat implementation plus test fakes. The binding flow is synchronous for QR initialization and request-driven for subsequent provider refresh during polling.

**Tech Stack:** Go 1.23, Hertz, SQLite, GORM with migrations, `testing`, `httptest`, `crypto/aes`, `crypto/cipher`, `crypto/sha256`, ULID

---

## File Structure

- `cmd/server/main.go`
  Starts the Hertz server, loads config, constructs dependencies, and registers routes.
- `internal/config/config.go`
  Reads environment-backed config such as SQLite path and `CHANNEL_MASTER_KEY`.
- `internal/api/http/router.go`
  Registers all `/api/v1` routes and middleware.
- `internal/api/http/request_id.go`
  Hertz middleware that creates a prefixed request ID and stores it in request context for every response envelope.
- `internal/api/http/handlers/*.go`
  HTTP handlers for channel bindings, channel accounts, runtime config, and shared response helpers.
- `internal/api/http/dto/*.go`
  Request and response DTOs so HTTP structs stay separate from domain entities.
- `internal/app/binding_service.go`
  Orchestrates create-binding and binding-detail refresh flow.
- `internal/app/app_key_service.go`
  Creates, rotates, disables, and validates `app_key` values.
- `internal/app/runtime_service.go`
  Resolves runtime configuration from `X-App-Key`.
- `internal/app/user_service.go`
  Resolves API `user_id` to internal user records.
- `internal/app/channel_account_query_service.go`
  Lists channel accounts for a user and attaches `has_active_app_key`.
- `internal/domain/*.go`
  Domain entities, statuses, repository interfaces, and typed errors.
- `internal/domain/ids.go`
  Prefixed ULID generation for resource IDs and request IDs.
- `internal/store/models/*.go`
  GORM models matching the SQLite schema.
- `internal/store/migrations/*.sql`
  Initial schema migrations for users, channel accounts, channel bindings, and app keys.
- `internal/store/repositories/*.go`
  Repository implementations for each aggregate.
- `internal/security/crypto.go`
  AES-GCM encryption and decryption for credential payloads.
- `internal/security/app_key.go`
  Random `app_key` generation, prefix extraction, and hashing.
- `internal/channel/provider.go`
  Channel provider contracts and runtime config contract.
- `internal/channel/wechat/provider.go`
  First WeChat provider implementation or stub adapter boundary.
- `internal/channel/wechat/fake_provider.go`
  Test-only fake behavior for deterministic binding and runtime config tests.
- `internal/bootstrap/bootstrap.go`
  Dependency assembly to keep `main.go` small.
- `internal/testutil/sqlite.go`
  Shared helpers for temporary SQLite DB setup in tests.
- `internal/testutil/http.go`
  Shared helpers for API integration tests.

### Task 1: Bootstrap Project Layout And Shared Contracts

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/bootstrap/bootstrap.go`
- Create: `internal/domain/errors.go`
- Create: `internal/domain/status.go`
- Create: `internal/domain/ids.go`
- Create: `internal/channel/provider.go`
- Create: `internal/api/http/response.go`
- Create: `internal/api/http/request_id.go`
- Create: `cmd/server/main.go`
- Modify: `go.mod`
- Test: `internal/config/config_test.go`
- Test: `internal/api/http/response_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestLoadConfigRequiresMasterKey(t *testing.T) {
    t.Setenv("CHANNEL_MASTER_KEY", "")
    _, err := Load()
    if err == nil {
        t.Fatal("expected error")
    }
}

func TestLoadConfigRejectsInvalidMasterKeyLength(t *testing.T) {
    t.Setenv("CHANNEL_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
    _, err := Load()
    if err == nil {
        t.Fatal("expected invalid key length error")
    }
}

func TestWriteOKWrapsEnvelope(t *testing.T) {
    rr := httptest.NewRecorder()
    WriteOK(rr, "req_1", map[string]string{"status": "ok"})
    if rr.Code != http.StatusOK {
        t.Fatalf("unexpected status: %d", rr.Code)
    }
}

func TestNewPrefixedIDUsesULIDShape(t *testing.T) {
    got := NewPrefixedID("bind")
    if !strings.HasPrefix(got, "bind_") {
        t.Fatalf("unexpected id: %s", got)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config ./internal/api/http`
Expected: FAIL with missing packages or undefined functions

- [ ] **Step 3: Write minimal implementation**

```go
type Config struct {
    HTTPAddr         string
    SQLitePath       string
    ChannelMasterKey string
}

func Load() (Config, error) { /* env parsing */ }
```

```go
func NewPrefixedID(prefix string) string { /* prefix + ULID */ }
```

```go
func RequestIDMiddleware() app.HandlerFunc {
    // generate req_ prefixed id, store in request context, and make response helpers read it
}
```

```go
type Envelope struct {
    Code      string      `json:"code"`
    Message   string      `json:"message"`
    RequestID string      `json:"request_id"`
    Data      interface{} `json:"data"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config ./internal/domain ./internal/api/http`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd/server/main.go internal/config/config.go internal/bootstrap/bootstrap.go internal/domain/errors.go internal/domain/status.go internal/domain/ids.go internal/channel/provider.go internal/api/http/response.go internal/api/http/request_id.go internal/config/config_test.go internal/api/http/response_test.go
git commit -m "feat: bootstrap channel management service contracts"
```

### Task 2: Add SQLite Schema And Store Models

**Files:**
- Create: `internal/store/migrations/0001_init.sql`
- Create: `internal/store/db.go`
- Create: `internal/store/models/user.go`
- Create: `internal/store/models/channel_account.go`
- Create: `internal/store/models/channel_binding.go`
- Create: `internal/store/models/app_key.go`
- Create: `internal/testutil/sqlite.go`
- Test: `internal/store/db_test.go`

- [ ] **Step 1: Write the failing migration test**

```go
func TestMigrateCreatesCoreTables(t *testing.T) {
    db := openTestDB(t)
    if err := Migrate(db); err != nil {
        t.Fatal(err)
    }
    assertTableExists(t, db, "users")
    assertTableExists(t, db, "channel_accounts")
    assertTableExists(t, db, "channel_bindings")
    assertTableExists(t, db, "app_keys")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store -run TestMigrateCreatesCoreTables -v`
Expected: FAIL with missing migration or helper implementation

- [ ] **Step 3: Write minimal implementation**

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    external_user_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE channel_accounts (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    account_uid TEXT NOT NULL,
    display_name TEXT NOT NULL,
    avatar_url TEXT NOT NULL DEFAULT '',
    credential_ciphertext BLOB NOT NULL,
    credential_version INTEGER NOT NULL,
    last_bound_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE channel_bindings (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_binding_ref TEXT NOT NULL DEFAULT '',
    qr_code_payload TEXT NOT NULL DEFAULT '',
    expires_at DATETIME,
    error_message TEXT NOT NULL DEFAULT '',
    channel_account_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    finished_at DATETIME
);
CREATE TABLE app_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    channel_account_id TEXT NOT NULL,
    app_key_hash TEXT NOT NULL,
    app_key_prefix TEXT NOT NULL,
    status TEXT NOT NULL,
    last_used_at DATETIME,
    created_at DATETIME NOT NULL,
    disabled_at DATETIME
);
CREATE UNIQUE INDEX idx_channel_accounts_unique_user_channel_uid
ON channel_accounts(user_id, channel_type, account_uid);
CREATE INDEX idx_app_keys_hash ON app_keys(app_key_hash);
```

```go
func Migrate(db *gorm.DB) error { /* apply 0001_init.sql */ }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store -run TestMigrateCreatesCoreTables -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/0001_init.sql internal/store/db.go internal/store/models/*.go internal/testutil/sqlite.go internal/store/db_test.go
git commit -m "feat: add sqlite schema for channel management"
```

### Task 3: Implement Repository Layer

**Files:**
- Create: `internal/domain/repositories.go`
- Create: `internal/store/repositories/user_repository.go`
- Create: `internal/store/repositories/channel_account_repository.go`
- Create: `internal/store/repositories/channel_binding_repository.go`
- Create: `internal/store/repositories/app_key_repository.go`
- Test: `internal/store/repositories/user_repository_test.go`
- Test: `internal/store/repositories/channel_account_repository_test.go`
- Test: `internal/store/repositories/channel_binding_repository_test.go`
- Test: `internal/store/repositories/app_key_repository_test.go`

- [ ] **Step 1: Write failing repository tests**

```go
func TestUserRepositoryFindOrCreateByExternalUserID(t *testing.T) {
    repo := newUserRepo(t)
    user1, _ := repo.FindOrCreateByExternalUserID(ctx, "u_123")
    user2, _ := repo.FindOrCreateByExternalUserID(ctx, "u_123")
    if user1.ID != user2.ID {
        t.Fatal("expected idempotent user resolution")
    }
}
```

```go
func TestChannelBindingRepositoryUpdateStatus(t *testing.T) {
    repo := newBindingRepo(t)
    binding := seedBinding(t, repo)
    got, _ := repo.UpdateStatus(ctx, binding.ID, domain.BindingStatusConfirmed)
    if got.Status != domain.BindingStatusConfirmed {
        t.Fatal("status not updated")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/repositories -v`
Expected: FAIL with undefined repository implementations

- [ ] **Step 3: Write minimal implementation**

```go
type UserRepository interface {
    FindOrCreateByExternalUserID(ctx context.Context, externalUserID string) (domain.User, error)
}
```

```go
type ChannelBindingRepository interface {
    Create(ctx context.Context, binding domain.ChannelBinding) (domain.ChannelBinding, error)
    GetByID(ctx context.Context, id string) (domain.ChannelBinding, error)
    Update(ctx context.Context, binding domain.ChannelBinding) (domain.ChannelBinding, error)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/repositories -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/repositories.go internal/store/repositories/*.go internal/store/repositories/*_test.go
git commit -m "feat: add repository layer for channel service"
```

### Task 4: Implement Security Primitives

**Files:**
- Create: `internal/security/crypto.go`
- Create: `internal/security/app_key.go`
- Test: `internal/security/crypto_test.go`
- Test: `internal/security/app_key_test.go`

- [ ] **Step 1: Write failing crypto and key tests**

```go
func TestEncryptDecryptRoundTrip(t *testing.T) {
    cipher := NewCipher(mustKey(t))
    plaintext := []byte(`{"session":"x"}`)
    ciphertext, _ := cipher.Encrypt(plaintext)
    roundTrip, _ := cipher.Decrypt(ciphertext)
    if string(roundTrip) != string(plaintext) {
        t.Fatal("round trip mismatch")
    }
}
```

```go
func TestGenerateAppKeyReturnsPrefixAndHash(t *testing.T) {
    key, prefix, hash, err := Generate()
    if err != nil || len(prefix) != 8 || len(hash) == 0 || len(key) == 0 {
        t.Fatal("unexpected key artifacts")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/security -v`
Expected: FAIL with missing implementations

- [ ] **Step 3: Write minimal implementation**

```go
func Generate() (plaintext string, prefix string, hash string, err error) {
    raw := make([]byte, 32)
    rand.Read(raw)
    plaintext = "appk_" + base64.RawURLEncoding.EncodeToString(raw)
    sum := sha256.Sum256([]byte(plaintext))
    return plaintext, plaintext[:8], hex.EncodeToString(sum[:]), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/security -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/security/crypto.go internal/security/app_key.go internal/security/crypto_test.go internal/security/app_key_test.go
git commit -m "feat: add credential encryption and app key primitives"
```

### Task 5: Implement Fake And Real WeChat Providers

**Files:**
- Create: `internal/channel/wechat/config.go`
- Create: `internal/channel/wechat/provider.go`
- Create: `internal/channel/wechat/client.go`
- Create: `internal/channel/wechat/fake_provider.go`
- Test: `internal/channel/wechat/provider_integration_test.go`
- Test: `internal/channel/wechat/fake_provider_test.go`

- [ ] **Step 1: Write failing provider contract tests**

```go
func TestFakeProviderCreateAndRefreshBinding(t *testing.T) {
    provider := NewFakeProvider()
    created, _ := provider.CreateBinding(ctx, channel.CreateBindingRequest{BindingID: "bind_1", ChannelType: "wechat"})
    refreshed, _ := provider.RefreshBinding(ctx, channel.RefreshBindingRequest{ProviderBindingRef: created.ProviderBindingRef})
    if refreshed.ProviderStatus != "qr_ready" {
        t.Fatalf("unexpected status: %s", refreshed.ProviderStatus)
    }
}

func TestRealProviderSkipsWithoutWechatIntegrationEnv(t *testing.T) {
    if os.Getenv("WECHAT_INTEGRATION_ENABLED") == "" {
        t.Skip("set WECHAT_INTEGRATION_ENABLED=1 to run real provider integration test")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/wechat -v`
Expected: FAIL with missing provider implementation

- [ ] **Step 3: Write minimal implementation**

```go
type FakeProvider struct {
    states map[string]channel.RefreshBindingResult
}
```

```go
type Client interface {
    CreateBindingSession(ctx context.Context, bindingID string) (CreateSessionResult, error)
    GetBindingSession(ctx context.Context, providerRef string) (GetSessionResult, error)
}
```

```go
func (p *FakeProvider) BuildRuntimeConfig(ctx context.Context, req channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
    return channel.RuntimeConfig{/* credential_blob + runtime_options */}, nil
}
```

```go
func (p *Provider) CreateBinding(ctx context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
    // call Client.CreateBindingSession and map result into provider contract
}
```

Implement the real provider behind `internal/channel/wechat/client.go` as an HTTP client to a reference-adapter process that exposes the same login/session semantics required by `m1heng/claude-plugin-weixin`. Minimal MVP done definition for the real provider:

- `CreateBinding` returns a real QR payload, provider binding reference, and expiry time
- `RefreshBinding` can return `qr_ready`, `confirmed`, `failed`, or `expired`
- `confirmed` returns real `account_uid`, display fields, and credential payload
- config is explicit in `internal/channel/wechat/config.go` with env such as `WECHAT_REFERENCE_BASE_URL` and optional auth token

Keep CI deterministic with `FakeProvider`, and keep real WeChat verification behind an opt-in integration test that requires the reference adapter environment.

Reference-adapter contract to implement against:

- `POST /api/v1/wechat/bindings`
  Request JSON: `{"binding_id":"bind_xxx"}`
  Response JSON:

```json
{
  "provider_binding_ref": "wxbind_xxx",
  "qr_code_payload": "weixin://...",
  "expires_at": "2026-04-03T12:00:00Z"
}
```

- `GET /api/v1/wechat/bindings/:provider_binding_ref`
  Response JSON:

```json
{
  "status": "qr_ready",
  "qr_code_payload": "weixin://...",
  "expires_at": "2026-04-03T12:00:00Z",
  "account_uid": "",
  "display_name": "",
  "avatar_url": "",
  "credential_payload": null,
  "credential_version": 1,
  "error_message": ""
}
```

Status mapping must be exact:

- adapter `qr_ready` -> provider `qr_ready`
- adapter `confirmed` -> provider `confirmed`
- adapter `failed` -> provider `failed`
- adapter `expired` -> provider `expired`

`credential_payload` must be JSON bytes encoded from the adapter response body field so `credential_version` stays meaningful across provider and runtime boundaries.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/channel/wechat -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/channel/wechat/config.go internal/channel/wechat/provider.go internal/channel/wechat/client.go internal/channel/wechat/fake_provider.go internal/channel/wechat/provider_integration_test.go internal/channel/wechat/fake_provider_test.go
git commit -m "feat: add real and fake wechat providers"
```

### Task 6: Implement Binding Application Service

**Files:**
- Create: `internal/app/binding_service.go`
- Test: `internal/app/binding_service_test.go`

- [ ] **Step 1: Write failing binding service tests**

```go
func TestCreateBindingCreatesUserAndReturnsQRReady(t *testing.T) {
    svc := newBindingService(t)
    got, err := svc.CreateBinding(ctx, CreateBindingInput{
        ExternalUserID: "u_123",
        ChannelType:    "wechat",
    })
    if err != nil || got.Status != domain.BindingStatusQRReady {
        t.Fatal("expected qr_ready binding")
    }
}
```

```go
func TestGetBindingDetailRefreshesProviderAndConfirmsAccount(t *testing.T) {
    svc := newBindingService(t)
    binding := createSeedBinding(t, svc)
    markProviderConfirmed(t, svc, binding.ProviderBindingRef)
    got, _ := svc.GetBindingDetail(ctx, binding.ID)
    if got.Status != domain.BindingStatusConfirmed || got.ChannelAccountID == "" {
        t.Fatal("expected confirmed binding")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run 'Test(CreateBinding|GetBindingDetail)' -v`
Expected: FAIL with undefined service implementation

- [ ] **Step 3: Write minimal implementation**

```go
type BindingService struct {
    users    domain.UserRepository
    bindings domain.ChannelBindingRepository
    accounts domain.ChannelAccountRepository
    cipher   security.Cipher
    provider channel.Provider
}
```

```go
func (s *BindingService) GetBindingDetail(ctx context.Context, id string) (BindingDetail, error) {
    // load binding, refresh provider, persist status changes, upsert account on confirmation
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app -run 'Test(CreateBinding|GetBindingDetail)' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/binding_service.go internal/app/binding_service_test.go
git commit -m "feat: add binding application service"
```

### Task 7: Implement App Key, Runtime, And Account Query Services

**Files:**
- Create: `internal/app/app_key_service.go`
- Create: `internal/app/runtime_service.go`
- Create: `internal/app/channel_account_query_service.go`
- Test: `internal/app/app_key_service_test.go`
- Test: `internal/app/runtime_service_test.go`
- Test: `internal/app/channel_account_query_service_test.go`

- [ ] **Step 1: Write failing service tests**

```go
func TestCreateAppKeyRotatesExistingKey(t *testing.T) {
    svc := newAppKeyService(t)
    first, _ := svc.CreateOrRotate(ctx, "acct_1")
    second, _ := svc.CreateOrRotate(ctx, "acct_1")
    if first.KeyID == second.KeyID {
        t.Fatal("expected rotation")
    }
}
```

```go
func TestRuntimeServiceReturnsConfigForValidAppKey(t *testing.T) {
    svc := newRuntimeService(t)
    key := seedActiveAppKey(t, svc)
    cfg, err := svc.GetByAppKey(ctx, key)
    if err != nil || cfg.ChannelType != "wechat" {
        t.Fatal("expected runtime config")
    }
}

func TestDisableByChannelAccountIDIsIdempotent(t *testing.T) {
    svc := newAppKeyService(t)
    if err := svc.Disable(ctx, DisableAppKeyInput{ChannelAccountID: "acct_1"}); err != nil {
        t.Fatal(err)
    }
}

func TestListAccountsIncludesHasActiveAppKey(t *testing.T) {
    svc := newChannelAccountQueryService(t)
    items, err := svc.ListByExternalUserID(ctx, "u_123", "wechat")
    if err != nil || len(items) == 0 {
        t.Fatal("expected account list")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run 'Test(CreateAppKey|RuntimeService)' -v`
Expected: FAIL with undefined services

- [ ] **Step 3: Write minimal implementation**

```go
func (s *AppKeyService) CreateOrRotate(ctx context.Context, channelAccountID string) (AppKeyResult, error) {
    // disable existing key, generate new key, persist hash + prefix, return plaintext once
}
```

```go
func (s *RuntimeService) GetByAppKey(ctx context.Context, appKey string) (channel.RuntimeConfig, error) {
    // hash key, load account, decrypt credentials, build runtime config, update last_used_at
}
```

```go
func (s *ChannelAccountQueryService) ListByExternalUserID(ctx context.Context, externalUserID string, channelType string) ([]ChannelAccountListItem, error) {
    // resolve user, list channel accounts, attach has_active_app_key
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app -run 'Test(CreateAppKey|RuntimeService|DisableByChannelAccountID|ListAccounts)' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/app_key_service.go internal/app/runtime_service.go internal/app/channel_account_query_service.go internal/app/app_key_service_test.go internal/app/runtime_service_test.go internal/app/channel_account_query_service_test.go
git commit -m "feat: add app key runtime and account query services"
```

### Task 8: Implement Hertz Handlers And Route Wiring

**Files:**
- Create: `internal/api/http/router.go`
- Create: `internal/api/http/handlers/channel_bindings.go`
- Create: `internal/api/http/handlers/channel_accounts.go`
- Create: `internal/api/http/handlers/runtime.go`
- Create: `internal/api/http/dto/channel_bindings.go`
- Create: `internal/api/http/dto/channel_accounts.go`
- Create: `internal/api/http/dto/runtime.go`
- Create: `internal/testutil/http.go`
- Test: `internal/api/http/handlers/channel_bindings_test.go`
- Test: `internal/api/http/handlers/channel_accounts_test.go`
- Test: `internal/api/http/handlers/runtime_test.go`
- Test: `internal/api/http/handlers/errors_test.go`

- [ ] **Step 1: Write failing handler tests**

```go
func TestCreateBindingHandlerReturnsEnvelope(t *testing.T) {
    ts := newHTTPServer(t)
    resp := postJSON(t, ts, "/api/v1/channel-bindings/create", `{"user_id":"u_123","channel_type":"wechat"}`)
    assertStatus(t, resp, http.StatusOK)
    assertJSONCode(t, resp, "OK")
}
```

```go
func TestDisableAppKeyRejectsBothIdentifiers(t *testing.T) {
    ts := newHTTPServer(t)
    resp := postJSON(t, ts, "/api/v1/channel-accounts/app-key/disable", `{"channel_account_id":"acct_1","key_id":"key_1"}`)
    assertJSONCode(t, resp, "INVALID_ARGUMENT")
}

func TestRuntimeConfigReturnsAppKeyDisabledCode(t *testing.T) {
    ts := newHTTPServer(t)
    resp := getWithHeader(t, ts, "/api/v1/runtime/config", "X-App-Key", "disabled-key")
    assertJSONCode(t, resp, "APP_KEY_DISABLED")
}

func TestBindingDetailReturnsOKWithExpiredStatus(t *testing.T) {
    ts := newHTTPServer(t)
    resp := getJSON(t, ts, "/api/v1/channel-bindings/detail?binding_id=expired")
    assertJSONCode(t, resp, "OK")
    assertJSONDataStatus(t, resp, "expired")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/http/... -v`
Expected: FAIL with missing router and handlers

- [ ] **Step 3: Write minimal implementation**

```go
func RegisterRoutes(h *server.Hertz, deps Dependencies) {
    v1 := h.Group("/api/v1")
    v1.POST("/channel-bindings/create", handlers.CreateBinding(deps.BindingService))
    v1.GET("/channel-bindings/detail", handlers.GetBindingDetail(deps.BindingService))
    v1.GET("/channel-accounts/list", handlers.ListChannelAccounts(deps.AccountQueryService))
    v1.POST("/channel-accounts/app-key/create", handlers.CreateAppKey(deps.AppKeyService))
    v1.POST("/channel-accounts/app-key/disable", handlers.DisableAppKey(deps.AppKeyService))
    v1.GET("/runtime/config", handlers.GetRuntimeConfig(deps.RuntimeService))
}
```

Add explicit domain-to-envelope error mapping for:

- `INVALID_ARGUMENT`
- `NOT_FOUND`
- `BINDING_EXPIRED`
- `BINDING_FAILED`
- `APP_KEY_NOT_FOUND`
- `APP_KEY_DISABLED`
- `INTERNAL_ERROR`

Keep `GET /channel-bindings/detail` polling-friendly: even when `data.status` is `failed` or `expired`, return envelope `code=OK` and represent terminal binding state inside `data.status`.

Endpoint code table to implement:

| Endpoint | Scenario | Envelope `code` |
|---|---|---|
| `POST /channel-bindings/create` | validation error | `INVALID_ARGUMENT` |
| `POST /channel-bindings/create` | provider init failure | `BINDING_FAILED` |
| `GET /channel-bindings/detail` | binding not found | `NOT_FOUND` |
| `GET /channel-bindings/detail` | binding exists with any lifecycle status | `OK` |
| `POST /channel-accounts/app-key/disable` | both identifiers present | `INVALID_ARGUMENT` |
| `GET /runtime/config` | app key missing or unknown | `APP_KEY_NOT_FOUND` |
| `GET /runtime/config` | app key disabled | `APP_KEY_DISABLED` |

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/http/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/http/router.go internal/api/http/handlers/*.go internal/api/http/dto/*.go internal/testutil/http.go internal/api/http/handlers/*_test.go
git commit -m "feat: add hertz handlers for channel management api"
```

### Task 9: Wire End-To-End Service Startup And Smoke Tests

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `internal/bootstrap/bootstrap.go`
- Create: `internal/bootstrap/bootstrap_test.go`
- Create: `README.md`

- [ ] **Step 1: Write failing bootstrap smoke tests**

```go
func TestBootstrapBuildsDependencies(t *testing.T) {
    cfg := testConfig(t)
    deps, err := bootstrap.New(cfg)
    if err != nil || deps.Router == nil {
        t.Fatal("expected complete dependency graph")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/bootstrap -v`
Expected: FAIL with incomplete wiring

- [ ] **Step 3: Write minimal implementation**

```go
func main() {
    cfg, err := config.Load()
    if err != nil {
        log.Fatal(err)
    }
    app, err := bootstrap.New(cfg)
    if err != nil {
        log.Fatal(err)
    }
    app.Server.Spin()
}
```

Update `README.md` with:

- required env vars
- local run command
- example API calls for create binding, polling detail, creating `app_key`, fetching runtime config

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/bootstrap -v && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go README.md
git commit -m "feat: wire channel management service startup"
```

## Verification Checklist

- [ ] `go test ./...`
- [ ] Start the server locally against a temp SQLite database
- [ ] `POST /api/v1/channel-bindings/create` returns `qr_ready`
- [ ] `GET /api/v1/channel-bindings/detail` refreshes provider state and persists updates
- [ ] `POST /api/v1/channel-accounts/app-key/create` returns plaintext `app_key` once
- [ ] `POST /api/v1/channel-accounts/app-key/disable` is idempotent and rejects both identifiers at once
- [ ] `GET /api/v1/runtime/config` reads `X-App-Key` and returns JSON `credential_blob.payload`
