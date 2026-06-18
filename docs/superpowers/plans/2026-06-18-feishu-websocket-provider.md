# Feishu WebSocket Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `feishu` channel that connects over Feishu's WebSocket long-connection, receives p2p + group-@mention messages, forwards them through the existing bot orchestrator, and replies back into Feishu — authenticated by a per-bot App ID / App Secret.

**Architecture:** New `internal/channel/feishu/` package implementing `channel.Provider` + `channel.RuntimeStarter` + `channel.ReplyGateway`, mirroring `wechat/` and `httpchan/`. All business logic (binding, message filtering, reply shaping) sits behind two interfaces — `feishuAPI` (validate creds, send text) and `dialer` (open the wss) — so it is unit-tested with fakes; the real SDK (`oapi-sdk-go/v3`) is isolated in two thin adapter files. Per-bot credentials thread from the connect endpoint through a new generic `Config map[string]string` on the binding requests, reusing the existing auto-confirm path (the same one `http` uses).

**Tech Stack:** Go 1.23, `net/http`, GORM/SQLite, `github.com/larksuite/oapi-sdk-go/v3` (Feishu SDK: low-level `larkws` + `EventDispatcher` for receive, `lark.Client.Im` for send), vanilla JS frontend.

**Receive-path note:** We use the SDK's **low-level** `larkws` + `EventDispatcher.OnP2MessageReceiveV1` (not the high-level `channel` module). This is the documented fallback recorded in the spec, chosen because group @mention detection needs `chat_type` + `mentions`, which the raw `im.message.receive_v1` event exposes reliably. Sending uses `lark.Client.Im.V1.Message.Create`/`Reply`.

---

## File Structure

**New files (`internal/channel/feishu/`):**
- `types.go` — shared types (`Credentials`, `AppInfo`, `InboundMessage`, `SendParams`) and the `feishuAPI` / `dialer` / `conn` interfaces.
- `registry.go` — process-local `map[botID]Credentials`, shared between runtime (writer) and reply gateway (reader). Keeps the App Secret in memory keyed by bot, so it never travels in reply metadata.
- `provider.go` — `Provider` implementing `channel.Provider` (CreateBinding/RefreshBinding/BuildRuntimeConfig).
- `runtime.go` — `Provider.StartRuntime` + the runtime handle + inbound filter/dispatch.
- `reply_gateway.go` — `ReplyGateway` implementing `channel.ReplyGateway`.
- `config.go` — `Config{Domain}` from `FEISHU_DOMAIN`.
- `api.go` — real `feishuAPI`: HTTP cred validation + `lark.Client` send (SDK touchpoint).
- `dialer.go` — real `dialer`: `larkws` + `EventDispatcher` (SDK touchpoint).
- `fake_test.go` — shared test fakes (`fakeAPI`, `fakeDialer`, `fakeConn`).
- `registry_test.go`, `provider_test.go`, `runtime_test.go`, `reply_gateway_test.go` — unit tests.

**Modified files:**
- `internal/channel/provider.go` — add `Config map[string]string` to `CreateBindingRequest` + `RefreshBindingRequest`.
- `internal/app/bot/service.go` — `StartBotLoginInput.Config`; thread it through `StartLogin` → `confirmAndStartRuntime` → `RefreshBindingRequest`; add `"feishu"` to `isAutoConfirmChannel`.
- `internal/api/http/dto/bots.go` — `ConnectBotRequest` gains `AppID` / `AppSecret`.
- `internal/api/http/handlers/bots.go` — `ConnectBot` maps creds into `StartBotLoginInput.Config`.
- `internal/bootstrap/bootstrap.go` — construct + register the feishu provider & reply gateway.
- `internal/api/http/web/static/index.html` — `feishu` channel option + App ID/Secret inputs in the connect card.
- `internal/api/http/web/static/app.js` — show connect card + cred inputs for feishu; post creds; auto-confirm reload.
- `go.mod` / `go.sum` — add the SDK dependency.

---

## Task 1: Generic `Config` on binding requests + service threading + feishu auto-confirm

**Files:**
- Modify: `internal/channel/provider.go` (`CreateBindingRequest` ~line 13, `RefreshBindingRequest` ~line 24)
- Modify: `internal/app/bot/service.go` (`StartBotLoginInput` ~115, `StartLogin` ~378/396, `confirmAndStartRuntime` ~440, `isAutoConfirmChannel` ~426)
- Test: `internal/app/bot/service_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/app/bot/service_test.go` (the `configRecordingProvider` is a local stub implementing `channel.Provider` + `channel.RuntimeStarter`):

```go
type configRecordingProvider struct {
	refreshConfig map[string]string
}

func (p *configRecordingProvider) CreateBinding(_ context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	return channel.CreateBindingResult{ProviderBindingRef: req.BindingID}, nil
}

func (p *configRecordingProvider) RefreshBinding(_ context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	p.refreshConfig = req.Config
	return channel.RefreshBindingResult{
		ProviderStatus:    "confirmed",
		AccountUID:        req.Config["app_id"],
		DisplayName:       "Feishu App",
		CredentialPayload: []byte(`{"app_id":"cli_x","app_secret":"s"}`),
		CredentialVersion: 1,
	}, nil
}

func (p *configRecordingProvider) BuildRuntimeConfig(_ context.Context, _ channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	return channel.RuntimeConfig{}, nil
}

func (p *configRecordingProvider) StartRuntime(_ context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{BotID: req.BotID, ChannelType: req.ChannelType, State: channel.RuntimeStateConnected})
	}
	return &recordingHandle{done: make(chan struct{})}, nil
}

type recordingHandle struct{ done chan struct{} }

func (h *recordingHandle) Stop()                 { close(h.done) }
func (h *recordingHandle) Done() <-chan struct{} { return h.done }

func TestStartLoginThreadsConfigForAutoConfirmChannel(t *testing.T) {
	provider := &configRecordingProvider{}
	svc := newTestBotServiceWithProvider(t, provider)
	ctx := context.Background()

	created, err := svc.CreateBot(ctx, CreateBotInput{ExternalUserID: "u1", Name: "feishu-bot", Type: domain.BotTypeChannel, ChannelType: "feishu"})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}

	out, err := svc.StartLogin(ctx, StartBotLoginInput{
		BotID:  created.BotID,
		Config: map[string]string{"app_id": "cli_x", "app_secret": "s"},
	})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if out.Status != "confirmed" {
		t.Fatalf("status = %q, want confirmed", out.Status)
	}
	if provider.refreshConfig["app_id"] != "cli_x" || provider.refreshConfig["app_secret"] != "s" {
		t.Fatalf("RefreshBinding did not receive config, got %#v", provider.refreshConfig)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/bot -run TestStartLoginThreadsConfigForAutoConfirmChannel`
Expected: COMPILE FAIL — `StartBotLoginInput` has no field `Config`, and (once added) `feishu` is not auto-confirm so `StartLogin` returns a non-confirmed status.

- [ ] **Step 3: Add the `Config` field to both binding requests**

In `internal/channel/provider.go`, add `Config` to both structs:

```go
type CreateBindingRequest struct {
	BindingID   string
	ChannelType string
	Config      map[string]string
}
```
```go
type RefreshBindingRequest struct {
	ProviderBindingRef string
	ChannelType        string
	Config             map[string]string
}
```

- [ ] **Step 4: Thread `Config` through the service and mark feishu auto-confirm**

In `internal/app/bot/service.go`:

Add the field to `StartBotLoginInput`:
```go
type StartBotLoginInput struct {
	BotID  string
	Config map[string]string
}
```

In `StartLogin`, pass `Config` into `CreateBinding`:
```go
	result, err := s.provider.CreateBinding(ctx, channel.CreateBindingRequest{
		BindingID:   bindingID,
		ChannelType: bot.ChannelType,
		Config:      input.Config,
	})
```

In `StartLogin`, pass `Config` into the auto-confirm call:
```go
	if isAutoConfirmChannel(bot.ChannelType) {
		completed, err := s.confirmAndStartRuntime(ctx, bot, binding, result, input.Config)
```

Update `confirmAndStartRuntime`'s signature and its `RefreshBinding` call:
```go
func (s *BotService) confirmAndStartRuntime(ctx context.Context, bot domain.Bot, binding domain.ChannelBinding, _ channel.CreateBindingResult, config map[string]string) (completedLogin, error) {
	refreshed, err := s.provider.RefreshBinding(ctx, channel.RefreshBindingRequest{
		ProviderBindingRef: binding.ProviderBindingRef,
		ChannelType:        bot.ChannelType,
		Config:             config,
	})
```

Extend `isAutoConfirmChannel`:
```go
func isAutoConfirmChannel(channelType string) bool {
	return channelType == "http" || channelType == "feishu"
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/app/bot -run TestStartLoginThreadsConfigForAutoConfirmChannel`
Expected: PASS

- [ ] **Step 6: Run package tests to confirm no regression**

Run: `go test ./internal/app/bot ./internal/channel/...`
Expected: PASS (wechat/httpchan ignore the new optional field)

- [ ] **Step 7: Commit**

```bash
git add internal/channel/provider.go internal/app/bot/service.go internal/app/bot/service_test.go
git commit -m "feat: thread per-binding Config and make feishu auto-confirm"
```

---

## Task 2: feishu package types + registry

**Files:**
- Create: `internal/channel/feishu/types.go`
- Create: `internal/channel/feishu/registry.go`
- Test: `internal/channel/feishu/registry_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/channel/feishu/registry_test.go`:

```go
package feishu

import "testing"

func TestRegistryRegisterLookupUnregister(t *testing.T) {
	r := NewRegistry()

	if _, ok := r.Lookup("bot1"); ok {
		t.Fatal("expected no creds before register")
	}

	r.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s", BotOpenID: "ou_bot"})
	got, ok := r.Lookup("bot1")
	if !ok || got.AppID != "cli_x" || got.BotOpenID != "ou_bot" {
		t.Fatalf("Lookup after register = %#v, ok=%v", got, ok)
	}

	r.Unregister("bot1")
	if _, ok := r.Lookup("bot1"); ok {
		t.Fatal("expected no creds after unregister")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestRegistryRegisterLookupUnregister`
Expected: COMPILE FAIL — package `feishu` does not exist yet.

- [ ] **Step 3: Create the types file**

Create `internal/channel/feishu/types.go`:

```go
package feishu

import "context"

// ChannelType is the registered channel-type string for Feishu.
const ChannelType = "feishu"

// Credentials are the per-bot Feishu self-built app credentials plus the
// bot's own open_id (used to detect @mentions in group chats). This is the
// shape persisted (encrypted) in the channel account credential payload.
type Credentials struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	BotOpenID string `json:"bot_open_id"`
}

// AppInfo is returned when validating credentials against the Feishu API.
type AppInfo struct {
	AppName   string
	BotOpenID string
}

// InboundMessage is a normalized inbound Feishu message.
type InboundMessage struct {
	MessageID        string
	ChatID           string
	ChatType         string // "p2p" | "group"
	SenderOpenID     string
	Text             string
	MentionedOpenIDs []string
}

// SendParams describes an outbound text message. A non-empty ReplyMessageID
// makes it a reply threaded under the original message.
type SendParams struct {
	ChatID         string
	Text           string
	ReplyMessageID string
}

// feishuAPI abstracts the Feishu REST surface so provider/reply logic can be
// tested without real network calls. The real implementation lives in api.go.
type feishuAPI interface {
	ValidateApp(ctx context.Context, appID, appSecret string) (AppInfo, error)
	SendText(ctx context.Context, creds Credentials, p SendParams) error
}

// dialer opens a Feishu WebSocket long-connection. The real implementation
// lives in dialer.go.
type dialer interface {
	Dial(creds Credentials, onMessage func(InboundMessage)) (conn, error)
}

// conn is one live long-connection. Start blocks until ctx is cancelled or a
// fatal error occurs; cancelling ctx is how the caller stops it.
type conn interface {
	Start(ctx context.Context) error
}
```

- [ ] **Step 4: Create the registry**

Create `internal/channel/feishu/registry.go`:

```go
package feishu

import "sync"

// Registry holds the in-memory credentials for every active feishu bot,
// keyed by bot ID. The runtime registers on start and unregisters on stop;
// the reply gateway looks creds up by bot ID. Keeping the App Secret here
// (not in reply metadata) keeps it out of events, logs, and agent context.
type Registry struct {
	mu    sync.RWMutex
	creds map[string]Credentials
}

func NewRegistry() *Registry {
	return &Registry{creds: make(map[string]Credentials)}
}

func (r *Registry) Register(botID string, creds Credentials) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creds[botID] = creds
}

func (r *Registry) Unregister(botID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.creds, botID)
}

func (r *Registry) Lookup(botID string) (Credentials, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.creds[botID]
	return c, ok
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/channel/feishu -run TestRegistryRegisterLookupUnregister`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/channel/feishu/types.go internal/channel/feishu/registry.go internal/channel/feishu/registry_test.go
git commit -m "feat: add feishu channel types and credential registry"
```

---

## Task 3: feishu Provider (binding) + shared test fakes

**Files:**
- Create: `internal/channel/feishu/provider.go`
- Create: `internal/channel/feishu/fake_test.go`
- Test: `internal/channel/feishu/provider_test.go`

- [ ] **Step 1: Write the shared test fakes**

Create `internal/channel/feishu/fake_test.go`:

```go
package feishu

import "context"

type fakeAPI struct {
	validateInfo    AppInfo
	validateErr     error
	validatedAppID  string
	validatedSecret string
	sent            []sentMessage
	sendErr         error
}

type sentMessage struct {
	creds  Credentials
	params SendParams
}

func (f *fakeAPI) ValidateApp(_ context.Context, appID, appSecret string) (AppInfo, error) {
	f.validatedAppID = appID
	f.validatedSecret = appSecret
	if f.validateErr != nil {
		return AppInfo{}, f.validateErr
	}
	return f.validateInfo, nil
}

func (f *fakeAPI) SendText(_ context.Context, creds Credentials, p SendParams) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, sentMessage{creds: creds, params: p})
	return nil
}

type fakeDialer struct {
	conn      *fakeConn
	dialErr   error
	lastCreds Credentials
}

func (d *fakeDialer) Dial(creds Credentials, onMessage func(InboundMessage)) (conn, error) {
	d.lastCreds = creds
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	d.conn.onMessage = onMessage
	return d.conn, nil
}

type fakeConn struct {
	onMessage func(InboundMessage)
	started   chan struct{}
}

func newFakeConn() *fakeConn { return &fakeConn{started: make(chan struct{})} }

func (c *fakeConn) Start(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

// inject simulates an inbound message arriving over the wire.
func (c *fakeConn) inject(msg InboundMessage) { c.onMessage(msg) }
```

- [ ] **Step 2: Write the failing provider test**

Create `internal/channel/feishu/provider_test.go`:

```go
package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/channel"
)

func newTestProvider(api feishuAPI) *Provider {
	return NewProvider(api, &fakeDialer{conn: newFakeConn()}, NewRegistry(), nil)
}

func TestCreateBindingReturnsRef(t *testing.T) {
	p := newTestProvider(&fakeAPI{})
	res, err := p.CreateBinding(context.Background(), channel.CreateBindingRequest{BindingID: "bind_1", ChannelType: ChannelType})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}
	if res.ProviderBindingRef != "bind_1" {
		t.Fatalf("ProviderBindingRef = %q, want bind_1", res.ProviderBindingRef)
	}
	if res.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt should be set")
	}
}

func TestRefreshBindingValidatesAndConfirms(t *testing.T) {
	api := &fakeAPI{validateInfo: AppInfo{AppName: "My App", BotOpenID: "ou_bot"}}
	p := newTestProvider(api)

	res, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ChannelType: ChannelType,
		Config:      map[string]string{"app_id": "cli_x", "app_secret": "secret"},
	})
	if err != nil {
		t.Fatalf("RefreshBinding: %v", err)
	}
	if api.validatedAppID != "cli_x" || api.validatedSecret != "secret" {
		t.Fatalf("ValidateApp got app=%q secret=%q", api.validatedAppID, api.validatedSecret)
	}
	if res.ProviderStatus != "confirmed" {
		t.Fatalf("status = %q, want confirmed", res.ProviderStatus)
	}
	if res.AccountUID != "cli_x" || res.DisplayName != "My App" {
		t.Fatalf("AccountUID=%q DisplayName=%q", res.AccountUID, res.DisplayName)
	}
	var creds Credentials
	if err := json.Unmarshal(res.CredentialPayload, &creds); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if creds.AppID != "cli_x" || creds.AppSecret != "secret" || creds.BotOpenID != "ou_bot" {
		t.Fatalf("credential payload = %#v", creds)
	}
}

func TestRefreshBindingMissingCredentials(t *testing.T) {
	p := newTestProvider(&fakeAPI{})
	_, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ChannelType: ChannelType,
		Config:      map[string]string{"app_id": "cli_x"},
	})
	if err == nil {
		t.Fatal("expected error for missing app_secret")
	}
}

func TestRefreshBindingValidateError(t *testing.T) {
	p := newTestProvider(&fakeAPI{validateErr: errors.New("bad creds")})
	_, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ChannelType: ChannelType,
		Config:      map[string]string{"app_id": "cli_x", "app_secret": "secret"},
	})
	if err == nil {
		t.Fatal("expected error when validation fails")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestRefreshBinding`
Expected: COMPILE FAIL — `NewProvider` / `Provider` undefined.

- [ ] **Step 4: Implement the provider**

Create `internal/channel/feishu/provider.go`:

```go
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/logging"
)

type Provider struct {
	api      feishuAPI
	dialer   dialer
	registry *Registry
	logger   *logging.Logger
}

func NewProvider(api feishuAPI, d dialer, registry *Registry, logger *logging.Logger) *Provider {
	return &Provider{api: api, dialer: d, registry: registry, logger: logger}
}

// CreateBinding is a no-op handshake: Feishu has no QR scan, so we return the
// binding ID immediately and let the auto-confirm path call RefreshBinding.
func (p *Provider) CreateBinding(_ context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	return channel.CreateBindingResult{
		ProviderBindingRef: req.BindingID,
		ExpiresAt:          time.Now().Add(5 * time.Minute),
	}, nil
}

// RefreshBinding validates the supplied App ID/Secret, then returns a
// confirmed binding carrying the credential payload to be encrypted and
// stored on the channel account.
func (p *Provider) RefreshBinding(ctx context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	appID := strings.TrimSpace(req.Config["app_id"])
	appSecret := strings.TrimSpace(req.Config["app_secret"])
	if appID == "" || appSecret == "" {
		return channel.RefreshBindingResult{}, fmt.Errorf("feishu: app_id and app_secret are required")
	}
	info, err := p.api.ValidateApp(ctx, appID, appSecret)
	if err != nil {
		return channel.RefreshBindingResult{}, fmt.Errorf("feishu validate app: %w", err)
	}
	payload, err := json.Marshal(Credentials{AppID: appID, AppSecret: appSecret, BotOpenID: info.BotOpenID})
	if err != nil {
		return channel.RefreshBindingResult{}, fmt.Errorf("feishu marshal credentials: %w", err)
	}
	return channel.RefreshBindingResult{
		ProviderStatus:    "confirmed",
		AccountUID:        appID,
		DisplayName:       info.AppName,
		CredentialPayload: payload,
		CredentialVersion: 1,
	}, nil
}

func (p *Provider) BuildRuntimeConfig(_ context.Context, req channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	return channel.RuntimeConfig{
		"credential_blob": map[string]any{"version": req.CredentialVersion},
	}, nil
}

var _ channel.Provider = (*Provider)(nil)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/channel/feishu -run 'TestCreateBinding|TestRefreshBinding'`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/channel/feishu/provider.go internal/channel/feishu/fake_test.go internal/channel/feishu/provider_test.go
git commit -m "feat: add feishu provider binding flow"
```

---

## Task 4: feishu runtime (StartRuntime + inbound filter/dispatch)

**Files:**
- Create: `internal/channel/feishu/runtime.go`
- Test: `internal/channel/feishu/runtime_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/channel/feishu/runtime_test.go`:

```go
package feishu

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/channel"
)

func startTestRuntime(t *testing.T) (*Provider, *fakeConn, *Registry, chan channel.RuntimeEvent, channel.RuntimeHandle) {
	t.Helper()
	fc := newFakeConn()
	registry := NewRegistry()
	p := NewProvider(&fakeAPI{}, &fakeDialer{conn: fc}, registry, nil)

	events := make(chan channel.RuntimeEvent, 4)
	payload, _ := json.Marshal(Credentials{AppID: "cli_x", AppSecret: "s", BotOpenID: "ou_bot"})
	handle, err := p.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot1",
		ChannelType:       ChannelType,
		CredentialPayload: payload,
		Callbacks: channel.RuntimeCallbacks{
			OnEvent: func(ev channel.RuntimeEvent) { events <- ev },
		},
	})
	if err != nil {
		t.Fatalf("StartRuntime: %v", err)
	}
	return p, fc, registry, events, handle
}

func TestStartRuntimeRegistersCreds(t *testing.T) {
	_, _, registry, _, handle := startTestRuntime(t)
	defer handle.Stop()
	if got, ok := registry.Lookup("bot1"); !ok || got.AppID != "cli_x" {
		t.Fatalf("registry Lookup = %#v ok=%v", got, ok)
	}
}

func TestRuntimeP2PMessageEmitsEvent(t *testing.T) {
	_, fc, _, events, handle := startTestRuntime(t)
	defer handle.Stop()
	fc.inject(InboundMessage{MessageID: "om_1", ChatID: "oc_1", ChatType: "p2p", SenderOpenID: "ou_user", Text: "hi"})

	select {
	case ev := <-events:
		if ev.Text != "hi" || ev.MessageID != "om_1" {
			t.Fatalf("event = %#v", ev)
		}
		if ev.ReplyTarget.MetadataValue("chat_id") != "oc_1" || ev.ReplyTarget.MetadataValue("chat_type") != "p2p" {
			t.Fatalf("reply metadata = %#v", ev.ReplyTarget.Metadata)
		}
		if ev.ReplyTarget.MetadataValue("bot_id") != "bot1" {
			t.Fatalf("bot_id metadata = %q", ev.ReplyTarget.MetadataValue("bot_id"))
		}
	case <-time.After(time.Second):
		t.Fatal("expected p2p event")
	}
}

func TestRuntimeGroupWithoutMentionIgnored(t *testing.T) {
	_, fc, _, events, handle := startTestRuntime(t)
	defer handle.Stop()
	fc.inject(InboundMessage{MessageID: "om_2", ChatID: "oc_2", ChatType: "group", SenderOpenID: "ou_user", Text: "hello", MentionedOpenIDs: []string{"ou_other"}})

	select {
	case ev := <-events:
		t.Fatalf("expected no event, got %#v", ev)
	case <-time.After(150 * time.Millisecond):
		// success: nothing delivered
	}
}

func TestRuntimeGroupWithMentionEmitsEventWithReplyID(t *testing.T) {
	_, fc, _, events, handle := startTestRuntime(t)
	defer handle.Stop()
	fc.inject(InboundMessage{MessageID: "om_3", ChatID: "oc_3", ChatType: "group", SenderOpenID: "ou_user", Text: "@bot hi", MentionedOpenIDs: []string{"ou_bot"}})

	select {
	case ev := <-events:
		if ev.ReplyTarget.MetadataValue("message_id") != "om_3" || ev.ReplyTarget.MetadataValue("chat_type") != "group" {
			t.Fatalf("reply metadata = %#v", ev.ReplyTarget.Metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("expected group @ event")
	}
}

func TestRuntimeStopUnregisters(t *testing.T) {
	_, _, registry, _, handle := startTestRuntime(t)
	handle.Stop()
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle did not finish")
	}
	if _, ok := registry.Lookup("bot1"); ok {
		t.Fatal("expected creds unregistered after Stop")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestRuntime`
Expected: COMPILE FAIL — `Provider.StartRuntime` undefined.

- [ ] **Step 3: Implement the runtime**

Create `internal/channel/feishu/runtime.go`:

```go
package feishu

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benenen/myclaw/internal/channel"
)

type runtimeHandle struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func (h *runtimeHandle) Stop()                 { h.cancel() }
func (h *runtimeHandle) Done() <-chan struct{} { return h.done }

func (p *Provider) StartRuntime(ctx context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	var creds Credentials
	if err := json.Unmarshal(req.CredentialPayload, &creds); err != nil {
		return nil, fmt.Errorf("feishu unmarshal credentials: %w", err)
	}
	if creds.AppID == "" || creds.AppSecret == "" {
		return nil, fmt.Errorf("feishu: runtime credentials missing app_id/app_secret")
	}

	onMessage := func(msg InboundMessage) {
		if !shouldHandle(msg, creds.BotOpenID) {
			return
		}
		if req.Callbacks.OnEvent == nil {
			return
		}
		req.Callbacks.OnEvent(channel.RuntimeEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			MessageID:   msg.MessageID,
			From:        msg.SenderOpenID,
			Text:        msg.Text,
			ReplyTarget: channel.ReplyTarget{
				ChannelType: req.ChannelType,
				RecipientID: msg.ChatID,
				Metadata: map[string]string{
					"bot_id":     req.BotID,
					"chat_id":    msg.ChatID,
					"chat_type":  msg.ChatType,
					"message_id": msg.MessageID,
				},
			},
		})
	}

	c, err := p.dialer.Dial(creds, onMessage)
	if err != nil {
		return nil, fmt.Errorf("feishu dial: %w", err)
	}

	p.registry.Register(req.BotID, creds)

	runtimeCtx, cancel := context.WithCancel(ctx)
	handle := &runtimeHandle{done: make(chan struct{}), cancel: cancel}

	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateConnected,
		})
	}

	go func() {
		defer close(handle.done)
		defer p.registry.Unregister(req.BotID)

		startErr := c.Start(runtimeCtx)
		if req.Callbacks.OnState == nil {
			return
		}
		if runtimeCtx.Err() != nil {
			req.Callbacks.OnState(channel.RuntimeStateEvent{
				BotID:       req.BotID,
				ChannelType: req.ChannelType,
				State:       channel.RuntimeStateStopped,
				Reason:      runtimeCtx.Err().Error(),
			})
			return
		}
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateError,
			Err:         startErr,
		})
	}()

	return handle, nil
}

// shouldHandle decides whether an inbound message should be forwarded:
// p2p always; group only when the bot itself is @mentioned. Empty-text
// messages (non-text payloads) are skipped.
func shouldHandle(msg InboundMessage, botOpenID string) bool {
	if msg.Text == "" {
		return false
	}
	if msg.ChatType == "p2p" {
		return true
	}
	if botOpenID == "" {
		return false
	}
	for _, id := range msg.MentionedOpenIDs {
		if id == botOpenID {
			return true
		}
	}
	return false
}

var _ channel.RuntimeStarter = (*Provider)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/channel/feishu -run TestRuntime`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/channel/feishu/runtime.go internal/channel/feishu/runtime_test.go
git commit -m "feat: add feishu websocket runtime with p2p and group-mention dispatch"
```

---

## Task 5: feishu ReplyGateway

**Files:**
- Create: `internal/channel/feishu/reply_gateway.go`
- Test: `internal/channel/feishu/reply_gateway_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/channel/feishu/reply_gateway_test.go`:

```go
package feishu

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

func p2pTarget() channel.ReplyTarget {
	return channel.ReplyTarget{
		ChannelType: ChannelType,
		RecipientID: "oc_1",
		Metadata:    map[string]string{"bot_id": "bot1", "chat_id": "oc_1", "chat_type": "p2p", "message_id": "om_1"},
	}
}

func groupTarget() channel.ReplyTarget {
	return channel.ReplyTarget{
		ChannelType: ChannelType,
		RecipientID: "oc_2",
		Metadata:    map[string]string{"bot_id": "bot1", "chat_id": "oc_2", "chat_type": "group", "message_id": "om_2"},
	}
}

func TestReplyP2PSendsToChatNoReplyID(t *testing.T) {
	api := &fakeAPI{}
	registry := NewRegistry()
	registry.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s"})
	g := NewReplyGateway(api, registry)

	if err := g.Reply(context.Background(), p2pTarget(), agent.Response{Text: "hello"}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(api.sent))
	}
	if api.sent[0].params.ChatID != "oc_1" || api.sent[0].params.ReplyMessageID != "" || api.sent[0].params.Text != "hello" {
		t.Fatalf("params = %#v", api.sent[0].params)
	}
	if api.sent[0].creds.AppID != "cli_x" {
		t.Fatalf("creds = %#v", api.sent[0].creds)
	}
}

func TestReplyGroupRepliesToMessage(t *testing.T) {
	api := &fakeAPI{}
	registry := NewRegistry()
	registry.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s"})
	g := NewReplyGateway(api, registry)

	if err := g.Reply(context.Background(), groupTarget(), agent.Response{Text: "yo"}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if api.sent[0].params.ReplyMessageID != "om_2" || api.sent[0].params.ChatID != "oc_2" {
		t.Fatalf("params = %#v", api.sent[0].params)
	}
}

func TestReplyBotNotActiveErrors(t *testing.T) {
	g := NewReplyGateway(&fakeAPI{}, NewRegistry())
	if err := g.Reply(context.Background(), p2pTarget(), agent.Response{Text: "hello"}); err == nil {
		t.Fatal("expected error when bot not registered")
	}
}

func TestReplyEmptyTextNoop(t *testing.T) {
	api := &fakeAPI{}
	registry := NewRegistry()
	registry.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s"})
	g := NewReplyGateway(api, registry)

	if err := g.Reply(context.Background(), p2pTarget(), agent.Response{Text: "   "}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(api.sent) != 0 {
		t.Fatalf("expected no send for empty text, got %d", len(api.sent))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestReply`
Expected: COMPILE FAIL — `NewReplyGateway` undefined.

- [ ] **Step 3: Implement the reply gateway**

Create `internal/channel/feishu/reply_gateway.go`:

```go
package feishu

import (
	"context"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type ReplyGateway struct {
	api      feishuAPI
	registry *Registry
}

func NewReplyGateway(api feishuAPI, registry *Registry) *ReplyGateway {
	return &ReplyGateway{api: api, registry: registry}
}

func (g *ReplyGateway) Reply(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error {
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil
	}
	botID := strings.TrimSpace(target.MetadataValue("bot_id"))
	if botID == "" {
		return fmt.Errorf("feishu reply: bot_id metadata required")
	}
	creds, ok := g.registry.Lookup(botID)
	if !ok {
		return fmt.Errorf("feishu reply: bot %q is not active", botID)
	}
	chatID := strings.TrimSpace(target.MetadataValue("chat_id"))
	if chatID == "" {
		chatID = strings.TrimSpace(target.RecipientID)
	}
	params := SendParams{ChatID: chatID, Text: text}
	if target.MetadataValue("chat_type") == "group" {
		params.ReplyMessageID = strings.TrimSpace(target.MetadataValue("message_id"))
	}
	if err := g.api.SendText(ctx, creds, params); err != nil {
		return fmt.Errorf("feishu reply: %w", err)
	}
	return nil
}

var _ channel.ReplyGateway = (*ReplyGateway)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/channel/feishu`
Expected: PASS (all feishu unit tests)

- [ ] **Step 5: Commit**

```bash
git add internal/channel/feishu/reply_gateway.go internal/channel/feishu/reply_gateway_test.go
git commit -m "feat: add feishu reply gateway"
```

---

## Task 6: Real SDK adapters (config, api, dialer) + dependency

**Files:**
- Create: `internal/channel/feishu/config.go`
- Create: `internal/channel/feishu/api.go`
- Create: `internal/channel/feishu/dialer.go`
- Modify: `go.mod`, `go.sum`

> **SDK touchpoint.** This is the only task that calls the real Feishu SDK. The exact builder/field names are taken from `oapi-sdk-go/v3`; Step 5 verifies them with `go build` and `go doc` and adjusts if a symbol differs. All logic was already tested with fakes in Tasks 3–5, so this task only needs to compile and pass `go build`.

- [ ] **Step 1: Add the dependency**

Run:
```bash
go get github.com/larksuite/oapi-sdk-go/v3@latest
```
Expected: `go.mod` gains `github.com/larksuite/oapi-sdk-go/v3`.

- [ ] **Step 2: Create the config file**

Create `internal/channel/feishu/config.go`:

```go
package feishu

import "os"

// Config holds environment-level (not per-bot) feishu settings. Per-bot App
// ID/Secret are supplied at connect time, not here.
type Config struct {
	// Domain is the Feishu/Lark API base URL. Feishu: https://open.feishu.cn
	// Lark (international): https://open.larksuite.com
	Domain string
}

func LoadConfig() Config {
	return Config{Domain: getEnvOrDefault("FEISHU_DOMAIN", "https://open.feishu.cn")}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 3: Create the real API adapter**

Create `internal/channel/feishu/api.go`:

```go
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// apiClient is the real feishuAPI. Credential validation uses plain REST
// (token + bot info); sending uses the SDK's lark.Client, cached per app_id
// so tenant-access-tokens are reused.
type apiClient struct {
	domain     string
	httpClient *http.Client

	mu      sync.Mutex
	clients map[string]*lark.Client
}

func NewAPI(cfg Config) *apiClient {
	return &apiClient{
		domain:     cfg.Domain,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		clients:    make(map[string]*lark.Client),
	}
}

func (a *apiClient) larkClient(appID, appSecret string) *lark.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.clients[appID]; ok {
		return c
	}
	c := lark.NewClient(appID, appSecret)
	a.clients[appID] = c
	return c
}

func (a *apiClient) ValidateApp(ctx context.Context, appID, appSecret string) (AppInfo, error) {
	token, err := a.tenantAccessToken(ctx, appID, appSecret)
	if err != nil {
		return AppInfo{}, err
	}
	return a.botInfo(ctx, token)
}

func (a *apiClient) tenantAccessToken(ctx context.Context, appID, appSecret string) (string, error) {
	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.domain+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("feishu auth failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return out.TenantAccessToken, nil
}

func (a *apiClient) botInfo(ctx context.Context, token string) (AppInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.domain+"/open-apis/bot/v3/info", nil)
	if err != nil {
		return AppInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return AppInfo{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			AppName string `json:"app_name"`
			OpenID  string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return AppInfo{}, err
	}
	if out.Code != 0 {
		return AppInfo{}, fmt.Errorf("feishu bot info failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return AppInfo{AppName: out.Bot.AppName, BotOpenID: out.Bot.OpenID}, nil
}

func (a *apiClient) SendText(ctx context.Context, creds Credentials, p SendParams) error {
	client := a.larkClient(creds.AppID, creds.AppSecret)
	content := larkim.NewTextMsgBuilder().Text(p.Text).Build()

	if p.ReplyMessageID != "" {
		resp, err := client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(p.ReplyMessageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeText).
				Content(content).
				Build()).
			Build())
		if err != nil {
			return err
		}
		if !resp.Success() {
			return fmt.Errorf("feishu reply failed: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return nil
	}

	resp, err := client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(p.ChatID).
			MsgType(larkim.MsgTypeText).
			Content(content).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("feishu send failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

var _ feishuAPI = (*apiClient)(nil)
```

- [ ] **Step 4: Create the real WS dialer**

Create `internal/channel/feishu/dialer.go`:

```go
package feishu

import (
	"context"
	"encoding/json"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type wsDialer struct{}

func NewDialer() *wsDialer { return &wsDialer{} }

func (wsDialer) Dial(creds Credentials, onMessage func(InboundMessage)) (conn, error) {
	handler := larkdispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			onMessage(normalizeMessage(event))
			return nil
		})
	cli := larkws.NewClient(creds.AppID, creds.AppSecret,
		larkws.WithEventHandler(handler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)
	return &wsConn{cli: cli}, nil
}

type wsConn struct {
	cli *larkws.Client
}

// Start runs the SDK's long-connection loop. It blocks until ctx is cancelled
// and reconnects internally on transient drops.
func (c *wsConn) Start(ctx context.Context) error {
	return c.cli.Start(ctx)
}

func normalizeMessage(event *larkim.P2MessageReceiveV1) InboundMessage {
	msg := event.Event.Message
	in := InboundMessage{
		MessageID: derefStr(msg.MessageId),
		ChatID:    derefStr(msg.ChatId),
		ChatType:  derefStr(msg.ChatType),
		Text:      parseTextContent(derefStr(msg.MessageType), derefStr(msg.Content)),
	}
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		in.SenderOpenID = derefStr(event.Event.Sender.SenderId.OpenId)
	}
	for _, m := range msg.Mentions {
		if m != nil && m.Id != nil {
			in.MentionedOpenIDs = append(in.MentionedOpenIDs, derefStr(m.Id.OpenId))
		}
	}
	return in
}

func parseTextContent(msgType, content string) string {
	if msgType != "text" {
		return ""
	}
	var c struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(content), &c)
	return c.Text
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ dialer = (*wsDialer)(nil)
```

- [ ] **Step 5: Build and verify SDK symbols**

Run: `go build ./internal/channel/feishu/`
Expected: SUCCESS.

If the build reports an unknown symbol, confirm the real name and adjust. Useful checks:
```bash
go doc github.com/larksuite/oapi-sdk-go/v3/service/im/v1 | grep -iE 'MsgTypeText|ReceiveIdTypeChatId|NewTextMsgBuilder|NewCreateMessageReqBuilder|NewReplyMessageReqBuilder'
go doc github.com/larksuite/oapi-sdk-go/v3/service/im/v1 P2MessageReceiveV1
go doc github.com/larksuite/oapi-sdk-go/v3/ws Client
go doc github.com/larksuite/oapi-sdk-go/v3/event/dispatcher EventDispatcher
```
Likely adjustment points if names differ: the `larkdispatcher.NewEventDispatcher(...).OnP2MessageReceiveV1` method, the `larkws.WithEventHandler` option, and the `msg.Mentions[].Id.OpenId` path. Fix to match `go doc` output; the package's tests still pass because they use the fakes.

- [ ] **Step 6: Run the feishu tests and tidy modules**

Run: `go mod tidy && go test ./internal/channel/feishu`
Expected: PASS, `go.sum` updated.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/channel/feishu/config.go internal/channel/feishu/api.go internal/channel/feishu/dialer.go
git commit -m "feat: add feishu SDK adapters for credential validation, send, and websocket receive"
```

---

## Task 7: Bootstrap wiring

**Files:**
- Modify: `internal/bootstrap/bootstrap.go` (imports ~line 18; provider construction ~70-89)

- [ ] **Step 1: Add the import**

In `internal/bootstrap/bootstrap.go`, add to the import block (next to the other channel imports):
```go
	"github.com/benenen/myclaw/internal/channel/feishu"
```

- [ ] **Step 2: Construct and register the feishu provider + reply gateway**

After the `httpProvider` / `httpReplyGateway` construction block (right before `multiProvider := channel.NewMultiProvider()`), add:
```go
	feishuRegistry := feishu.NewRegistry()
	feishuAPI := feishu.NewAPI(feishu.LoadConfig())
	feishuProvider := feishu.NewProvider(feishuAPI, feishu.NewDialer(), feishuRegistry, logger)
	feishuReplyGateway := feishu.NewReplyGateway(feishuAPI, feishuRegistry)
```

In the provider registration block, add:
```go
	multiProvider.Register("feishu", feishuProvider, feishuProvider)
```

In the reply-gateway registration block, add:
```go
	multiReplyGateway.Register("feishu", feishuReplyGateway)
```

- [ ] **Step 3: Build and run the suite**

Run: `go build ./... && go test ./internal/bootstrap ./internal/channel/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/bootstrap/bootstrap.go
git commit -m "feat: wire feishu provider and reply gateway into bootstrap"
```

---

## Task 8: DTO + handler threading for App ID / App Secret

**Files:**
- Modify: `internal/api/http/dto/bots.go` (`ConnectBotRequest` ~line 47)
- Modify: `internal/api/http/handlers/bots.go` (`ConnectBot` ~line 151)
- Test: `internal/api/http/handlers/bots_test.go`

- [ ] **Step 1: Write the failing test**

This goes through the real handler mux, mirroring the existing `newTestServerWithProvider` helper and `testutil.PostJSON` already used in `bots_test.go`. Add to `internal/api/http/handlers/bots_test.go`. First add two imports to the file's import block: `"context"` and `"github.com/benenen/myclaw/internal/channel"`. Then add:

```go
type connectConfigRecorder struct {
	refreshConfig map[string]string
}

func (p *connectConfigRecorder) CreateBinding(_ context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	return channel.CreateBindingResult{ProviderBindingRef: req.BindingID}, nil
}
func (p *connectConfigRecorder) RefreshBinding(_ context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	p.refreshConfig = req.Config
	return channel.RefreshBindingResult{ProviderStatus: "confirmed", AccountUID: req.Config["app_id"], CredentialPayload: []byte(`{"app_id":"cli_x"}`), CredentialVersion: 1}, nil
}
func (p *connectConfigRecorder) BuildRuntimeConfig(_ context.Context, _ channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	return channel.RuntimeConfig{}, nil
}
func (p *connectConfigRecorder) StartRuntime(_ context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{BotID: req.BotID, ChannelType: req.ChannelType, State: channel.RuntimeStateConnected})
	}
	return connectStubHandle{done: make(chan struct{})}, nil
}

type connectStubHandle struct{ done chan struct{} }

func (h connectStubHandle) Stop()                 {}
func (h connectStubHandle) Done() <-chan struct{} { return h.done }

// newTestServerWithCustomProvider mirrors newTestServerWithProvider but lets
// the test inject any channel.Provider.
func newTestServerWithCustomProvider(t *testing.T, provider channel.Provider) stdhttp.Handler {
	t.Helper()
	db := testutil.OpenTestDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, _ := security.NewCipher(key)
	userRepo := repositories.NewUserRepository(db)
	bindingRepo := repositories.NewChannelBindingRepository(db)
	accountRepo := repositories.NewChannelAccountRepository(db)
	botRepo := repositories.NewBotRepository(db)
	starter, _ := provider.(channel.RuntimeStarter)
	mux := stdhttp.NewServeMux()
	RegisterRoutes(mux, Dependencies{
		BotService: bot.NewBotService(userRepo, botRepo, bindingRepo, accountRepo, repositories.NewAgentCapabilityRepository(db), cipher, provider, bot.NewBotConnectionManagerWithCipher(botRepo, accountRepo, starter, cipher, nil)),
	})
	return mux
}

func TestConnectBotPassesFeishuCredentials(t *testing.T) {
	provider := &connectConfigRecorder{}
	ts := newTestServerWithCustomProvider(t, provider)

	createRes := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_1","name":"feishu-bot","type":"channel","channel_type":"feishu"}`)
	if createRes.Code != stdhttp.StatusOK {
		t.Fatalf("create status = %d body=%s", createRes.Code, createRes.Body.String())
	}
	var createEnv httpapi.Envelope
	if err := json.Unmarshal(createRes.Body.Bytes(), &createEnv); err != nil {
		t.Fatal(err)
	}
	botID := createEnv.Data.(map[string]any)["bot_id"].(string)

	connRes := testutil.PostJSON(t, ts, "/api/v1/bots/connect", `{"bot_id":"`+botID+`","app_id":"cli_x","app_secret":"secret"}`)
	if connRes.Code != stdhttp.StatusOK {
		t.Fatalf("connect status = %d body=%s", connRes.Code, connRes.Body.String())
	}
	if provider.refreshConfig["app_id"] != "cli_x" || provider.refreshConfig["app_secret"] != "secret" {
		t.Fatalf("provider did not receive creds: %#v", provider.refreshConfig)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/http/handlers -run TestConnectBotPassesFeishuCredentials`
Expected: FAIL — `ConnectBotRequest` has no `AppID`/`AppSecret`, so creds are dropped and `refreshConfig` is empty/nil.

- [ ] **Step 3: Extend the DTO**

In `internal/api/http/dto/bots.go`:
```go
type ConnectBotRequest struct {
	BotID     string `json:"bot_id"`
	AppID     string `json:"app_id,omitempty"`
	AppSecret string `json:"app_secret,omitempty"`
}
```

- [ ] **Step 4: Map creds into the login input**

In `internal/api/http/handlers/bots.go`, inside `ConnectBot`, replace the `StartLogin` call:
```go
		input := botapp.StartBotLoginInput{BotID: req.BotID}
		if req.AppID != "" || req.AppSecret != "" {
			input.Config = map[string]string{
				"app_id":     req.AppID,
				"app_secret": req.AppSecret,
			}
		}
		result, err := svc.StartLogin(r.Context(), input)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/http/handlers -run TestConnectBotPassesFeishuCredentials`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/http/dto/bots.go internal/api/http/handlers/bots.go internal/api/http/handlers/bots_test.go
git commit -m "feat: accept feishu app credentials on the connect endpoint"
```

---

## Task 9: Frontend — channel option, connect-card credential inputs, auto-confirm

**Files:**
- Modify: `internal/api/http/web/static/index.html` (channel select ~203; connect card ~141)
- Modify: `internal/api/http/web/static/app.js` (`renderDetail` ~268-296; `connectSelectedBot` ~364-383)

- [ ] **Step 1: Add the feishu channel option**

In `internal/api/http/web/static/index.html`, update the channel select (currently `wechat`/`http`):
```html
        <select id="create-bot-channel">
          <option value="wechat">wechat</option>
          <option value="http">http</option>
          <option value="feishu">feishu</option>
        </select>
```

- [ ] **Step 2: Add the credential inputs to the connect card**

In `internal/api/http/web/static/index.html`, inside `<div class="card" id="detail-connect-card">`, between the `detail-connect-hint` paragraph and the `Login / Connect` button, insert:
```html
          <div id="detail-feishu-fields" style="display:none;">
            <div class="form-field wide">
              <label>App ID</label>
              <input id="feishu-app-id" type="text" placeholder="cli_xxx">
            </div>
            <div class="form-field wide">
              <label>App Secret</label>
              <input id="feishu-app-secret" type="password" placeholder="app secret">
            </div>
          </div>
```

- [ ] **Step 3: Show the connect card + inputs for feishu in `renderDetail`**

In `internal/api/http/web/static/app.js`, in `renderDetail`:

Add the feishu flag next to the others:
```js
  const isFeishuChannel = bot.type === 'channel' && bot.channel_type === 'feishu';
```

Include feishu in the connect-card visibility:
```js
  const showConnect = isWeChatChannel || isHttpChannel || isFeishuChannel;
```

Toggle the feishu fields and set the hint (extend the existing hint if/else):
```js
  document.getElementById('detail-feishu-fields').style.display = isFeishuChannel ? '' : 'none';
  if (isHttpChannel) {
    document.getElementById('detail-connect-hint').textContent = 'Connect this bot to start chatting.';
  } else if (isWeChatChannel) {
    document.getElementById('detail-connect-hint').textContent = 'Generate a WeChat login QR and link this bot to an account.';
  } else if (isFeishuChannel) {
    document.getElementById('detail-connect-hint').textContent = 'Enter your Feishu self-built app credentials (App ID + App Secret) to connect.';
  }
```

- [ ] **Step 4: Post credentials + auto-confirm reload in `connectSelectedBot`**

In `internal/api/http/web/static/app.js`, replace `connectSelectedBot` with:
```js
async function connectSelectedBot() {
  if (!selectedBotId) { toast('select a bot'); return; }
  const bot = selectedBot();
  const body = { bot_id: selectedBotId };
  if (bot && bot.channel_type === 'feishu') {
    const appId = document.getElementById('feishu-app-id').value.trim();
    const appSecret = document.getElementById('feishu-app-secret').value.trim();
    if (!appId || !appSecret) { toast('app_id and app_secret required'); return; }
    body.app_id = appId;
    body.app_secret = appSecret;
  }
  const data = await api('POST', '/bots/connect', body);
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  const result = data.data;
  activeBindingId = result.binding_id;
  document.getElementById('connect-result').replaceChildren();

  // HTTP and Feishu channels auto-confirm — reload immediately.
  if (bot && bot.type === 'channel' && (bot.channel_type === 'http' || bot.channel_type === 'feishu')) {
    await loadBots();
    return;
  }

  if (result.qr_code_payload) {
    showQRModal(result.qr_code_payload, result.qr_share_url, result.status);
  }
  startLoginPolling(result.binding_id);
}
```

> Note: the original cleared `connect-result` via `.innerHTML = ''`; `.replaceChildren()` is the equivalent safe DOM clear and avoids an innerHTML lint warning. Functionally identical (empties the node).

- [ ] **Step 5: Verify the static assets still embed/build**

Run: `go build ./... && go test ./internal/api/http/...`
Expected: PASS (the embed test still finds the static files).

- [ ] **Step 6: Commit**

```bash
git add internal/api/http/web/static/index.html internal/api/http/web/static/app.js
git commit -m "feat: add feishu channel option and credential connect UI"
```

---

## Task 10: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: no findings

- [ ] **Step 4: Manual smoke (requires a real Feishu self-built app)**

This step needs real credentials and event-subscription + `im:message` permissions enabled in the Feishu developer console, with "long connection" mode selected for event delivery. If credentials are unavailable, note it and skip.

```bash
export CHANNEL_MASTER_KEY=$(openssl rand -base64 32)
go run ./cmd/server
```
Then in the UI:
1. Create a bot, type = Channel, channel = `feishu`.
2. Open the bot, enter App ID + App Secret in the connect card, click Login / Connect.
3. Expect the bot status to flip to `connected` (auto-confirm).
4. DM the bot in Feishu → expect a reply. In a group, @mention the bot → expect a threaded reply; a non-@ group message → no reply.

- [ ] **Step 5: Final commit (if any verification fixes were needed)**

```bash
git add -A
git commit -m "chore: verification fixes for feishu provider"
```

---

## Self-Review Notes

- **Spec coverage:** package structure (Task 2–6), provider/runtime/reply-gateway (3/4/5), per-bot credential threading via generic `Config` (1, 8), auto-confirm reuse (1), group @mention filtering (4), secret-never-in-metadata (registry in 2/4/5), bootstrap wiring (7), frontend (9), dependency (6), tests behind fakes (3–5). All spec sections map to a task.
- **Receive path:** committed to the spec's documented low-level fallback (larkws + dispatcher) because group @ needs `chat_type`/`mentions`; recorded at the top of this plan.
- **Type consistency:** `Credentials`, `AppInfo`, `InboundMessage`, `SendParams`, `feishuAPI`, `dialer`, `conn` are defined once in `types.go` (Task 2) and used unchanged in Tasks 3–6. `Config map[string]string` keys `app_id`/`app_secret` are identical across Tasks 1, 3, 8, 9. `shouldHandle` is a package-level function used only by the runtime.
- **SDK risk isolated:** only Task 6 touches the SDK; its Step 5 verifies symbol names with `go build` + `go doc`. All logic is fake-tested and independent of SDK symbol exactness.
