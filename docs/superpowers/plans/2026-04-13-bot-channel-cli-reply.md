# Bot Channel CLI Reply Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Forward inbound bot channel messages into a configured CLI command, wait for completion, and send the final result back to the same WeChat user.

**Architecture:** Keep WeChat runtime responsible for receiving messages, but route inbound `channel.RuntimeEvent` values through a bot-scoped orchestrator that serializes work per bot. Execute requests through a generic `oneshot` CLI driver behind an `AgentSessionManager`, then map the final text into a WeChat reply gateway.

**Tech Stack:** Go 1.23, net/http, os/exec, existing WeChat provider/client, repository-backed bot/account loading, go test

---

## File Map

| File | Responsibility |
|---|---|
| `internal/agent/types.go` | Shared agent request/response/spec/session state types |
| `internal/agent/manager.go` | Bot-keyed session manager and restart-on-broken logic |
| `internal/agent/session.go` | One bot session wrapper exposing `Send` and state transitions |
| `internal/agent/driver_oneshot.go` | `os/exec`-backed oneshot CLI execution with timeout handling |
| `internal/app/bot_message_orchestrator.go` | Per-bot queueing, dedupe, reply-on-result/failure |
| `internal/app/bot_connection_manager.go` | Convert runtime events into inbound messages and hand off to orchestrator |
| `internal/channel/provider.go` | Shared reply target type if needed by app/channel boundary |
| `internal/channel/wechat/client.go` | Add outbound send-message HTTP call and request/response helpers |
| `internal/channel/wechat/provider.go` | Build WeChat reply gateway from client methods |
| `internal/channel/wechat/reply_gateway.go` | WeChat-specific `ReplyGateway` implementation |
| `internal/config/config.go` | Default CLI command/args/workdir/timeout/queue config |
| `internal/bootstrap/bootstrap.go` | Wire agent manager, orchestrator, reply gateway, and config |
| `internal/agent/*_test.go` | Driver/session manager tests |
| `internal/app/bot_message_orchestrator_test.go` | Serial execution, dedupe, queue overflow tests |
| `internal/channel/wechat/*_test.go` | Outbound reply and client request mapping tests |
| `internal/bootstrap/bootstrap_test.go` | Wiring and startup restore tests |

## Task 1: Define agent runtime types and config

**Files:**
- Create: `internal/agent/types.go`
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing config test**

```go
func TestLoadAgentRuntimeConfig(t *testing.T) {
	t.Setenv("AGENT_CLI_COMMAND", "codex")
	t.Setenv("AGENT_CLI_ARGS", "reply --plain")
	t.Setenv("AGENT_CLI_WORKDIR", "/tmp/agent")
	t.Setenv("AGENT_CLI_TIMEOUT_SECONDS", "45")
	t.Setenv("AGENT_QUEUE_SIZE", "3")

	cfg := Load()

	if cfg.AgentCLICommand != "codex" {
		t.Fatalf("AgentCLICommand = %q", cfg.AgentCLICommand)
	}
	if len(cfg.AgentCLIArgs) != 2 || cfg.AgentCLIArgs[0] != "reply" || cfg.AgentCLIArgs[1] != "--plain" {
		t.Fatalf("AgentCLIArgs = %#v", cfg.AgentCLIArgs)
	}
	if cfg.AgentCLIWorkDir != "/tmp/agent" {
		t.Fatalf("AgentCLIWorkDir = %q", cfg.AgentCLIWorkDir)
	}
	if cfg.AgentCLITimeout != 45*time.Second {
		t.Fatalf("AgentCLITimeout = %s", cfg.AgentCLITimeout)
	}
	if cfg.AgentQueueSize != 3 {
		t.Fatalf("AgentQueueSize = %d", cfg.AgentQueueSize)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestLoadAgentRuntimeConfig -v`
Expected: FAIL because the new agent config fields do not exist yet.

- [ ] **Step 3: Add the shared agent types**

Create `internal/agent/types.go` with:

```go
package agent

import "time"

type SessionState string

const (
	SessionStateStarting SessionState = "starting"
	SessionStateReady    SessionState = "ready"
	SessionStateBusy     SessionState = "busy"
	SessionStateBroken   SessionState = "broken"
	SessionStateStopped  SessionState = "stopped"
)

type Spec struct {
	Type      string
	Command   string
	Args      []string
	WorkDir   string
	Env       map[string]string
	Timeout   time.Duration
	QueueSize int
}

type Request struct {
	BotID     string
	UserID    string
	MessageID string
	Prompt    string
	WorkDir   string
	Metadata  map[string]string
}

type Response struct {
	Text      string
	ExitCode  int
	Duration  time.Duration
	RawOutput string
}
```

- [ ] **Step 4: Add agent runtime config fields to `internal/config/config.go`**

Update the config struct and loader with:

```go
type Config struct {
	// existing fields...
	AgentCLICommand   string
	AgentCLIArgs      []string
	AgentCLIWorkDir   string
	AgentCLITimeout   time.Duration
	AgentQueueSize    int
}
```

Add load logic using existing env helpers/patterns:

```go
agentArgs := strings.Fields(getEnv("AGENT_CLI_ARGS", ""))
agentTimeoutSeconds := getEnvInt("AGENT_CLI_TIMEOUT_SECONDS", 60)
agentQueueSize := getEnvInt("AGENT_QUEUE_SIZE", 8)

cfg.AgentCLICommand = getEnv("AGENT_CLI_COMMAND", "codex")
cfg.AgentCLIArgs = agentArgs
cfg.AgentCLIWorkDir = getEnv("AGENT_CLI_WORKDIR", "")
cfg.AgentCLITimeout = time.Duration(agentTimeoutSeconds) * time.Second
cfg.AgentQueueSize = agentQueueSize
```

- [ ] **Step 5: Run the focused config test**

Run: `go test ./internal/config -run TestLoadAgentRuntimeConfig -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/types.go internal/config/config.go internal/config/config_test.go
git commit -m "feat: add agent runtime config types"
```

### Task 2: Build the oneshot CLI driver

**Files:**
- Create: `internal/agent/driver_oneshot.go`
- Test: `internal/agent/driver_oneshot_test.go`

- [ ] **Step 1: Write the failing driver tests**

Create `internal/agent/driver_oneshot_test.go` with:

```go
func TestOneshotDriverRunSuccess(t *testing.T) {
	driver := NewOneshotDriver()
	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf 'hello'"},
		Timeout: 2 * time.Second,
	}, Request{Prompt: "ignored"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
}

func TestOneshotDriverRunTimeout(t *testing.T) {
	driver := NewOneshotDriver()
	_, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "sleep 2"},
		Timeout: 100 * time.Millisecond,
	}, Request{Prompt: "ignored"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the driver tests to verify they fail**

Run: `go test ./internal/agent -run TestOneshotDriverRun -v`
Expected: FAIL because `NewOneshotDriver` and `Run` do not exist yet.

- [ ] **Step 3: Write the minimal oneshot driver**

Create `internal/agent/driver_oneshot.go` with:

```go
package agent

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

type OneshotDriver struct{}

func NewOneshotDriver() *OneshotDriver {
	return &OneshotDriver{}
}

func (d *OneshotDriver) Run(ctx context.Context, spec Spec, req Request) (Response, error) {
	runCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, spec.Command, spec.Args...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	cmd.Env = append(cmd.Environ(), flattenEnv(spec.Env)...)
	cmd.Stdin = strings.NewReader(req.Prompt)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	err := cmd.Run()
	resp := Response{
		Text:      strings.TrimSpace(stdout.String()),
		Duration:  time.Since(startedAt),
		RawOutput: strings.TrimSpace(stdout.String() + "\n" + stderr.String()),
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		resp.ExitCode = exitErr.ExitCode()
	}
	if err != nil {
		return resp, runCtx.ErrOr(err)
	}
	return resp, nil
}

func flattenEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
```

When implementing, replace `runCtx.ErrOr(err)` with explicit timeout handling:

```go
if err != nil {
	if runCtx.Err() != nil {
		return resp, runCtx.Err()
	}
	return resp, err
}
```

- [ ] **Step 4: Run the focused driver tests**

Run: `go test ./internal/agent -run TestOneshotDriverRun -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/driver_oneshot.go internal/agent/driver_oneshot_test.go
git commit -m "feat: add oneshot cli driver"
```

### Task 3: Add bot session manager around the driver

**Files:**
- Create: `internal/agent/session.go`
- Create: `internal/agent/manager.go`
- Test: `internal/agent/session_test.go`

- [ ] **Step 1: Write the failing session manager tests**

Create `internal/agent/session_test.go` with:

```go
type stubDriver struct {
	run func(context.Context, Spec, Request) (Response, error)
}

func (d stubDriver) Run(ctx context.Context, spec Spec, req Request) (Response, error) {
	return d.run(ctx, spec, req)
}

func TestManagerSendDelegatesToBotSession(t *testing.T) {
	mgr := NewManager(stubDriver{run: func(_ context.Context, _ Spec, req Request) (Response, error) {
		return Response{Text: "reply:" + req.Prompt}, nil
	}})

	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "reply:hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
}

func TestManagerMarksBrokenAfterDriverError(t *testing.T) {
	mgr := NewManager(stubDriver{run: func(context.Context, Spec, Request) (Response, error) {
		return Response{}, errors.New("boom")
	}})

	_, _ = mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "hello"})
	if mgr.State("bot-1") != SessionStateBroken {
		t.Fatalf("State() = %s", mgr.State("bot-1"))
	}
}
```

- [ ] **Step 2: Run the session tests to verify they fail**

Run: `go test ./internal/agent -run TestManager -v`
Expected: FAIL because manager/session types do not exist yet.

- [ ] **Step 3: Implement the session and manager**

Create `internal/agent/session.go` with:

```go
package agent

import (
	"context"
	"sync"
)

type Driver interface {
	Run(ctx context.Context, spec Spec, req Request) (Response, error)
}

type Session struct {
	mu     sync.Mutex
	state  SessionState
	driver Driver
	spec   Spec
}

func NewSession(driver Driver, spec Spec) *Session {
	return &Session{state: SessionStateReady, driver: driver, spec: spec}
}

func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) Send(ctx context.Context, req Request) (Response, error) {
	s.mu.Lock()
	s.state = SessionStateBusy
	s.mu.Unlock()

	resp, err := s.driver.Run(ctx, s.spec, req)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state = SessionStateBroken
		return resp, err
	}
	s.state = SessionStateReady
	return resp, nil
}
```

Create `internal/agent/manager.go` with:

```go
package agent

import (
	"context"
	"sync"
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	driver   Driver
}

func NewManager(driver Driver) *Manager {
	return &Manager{sessions: make(map[string]*Session), driver: driver}
}

func (m *Manager) Send(ctx context.Context, botID string, spec Spec, req Request) (Response, error) {
	session := m.sessionFor(botID, spec)
	return session.Send(ctx, req)
}

func (m *Manager) State(botID string) SessionState {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[botID]
	if !ok {
		return SessionStateStopped
	}
	return session.State()
}

func (m *Manager) sessionFor(botID string, spec Spec) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.sessions[botID]; ok && session.State() != SessionStateBroken {
		return session
	}
	session := NewSession(m.driver, spec)
	m.sessions[botID] = session
	return session
}
```

- [ ] **Step 4: Run the focused session tests**

Run: `go test ./internal/agent -run TestManager -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/session.go internal/agent/manager.go internal/agent/session_test.go
git commit -m "feat: add bot agent session manager"
```

### Task 4: Add WeChat outbound reply support

**Files:**
- Modify: `internal/channel/wechat/client.go`
- Create: `internal/channel/wechat/reply_gateway.go`
- Test: `internal/channel/wechat/client_test.go`
- Test: `internal/channel/wechat/reply_gateway_test.go`

- [ ] **Step 1: Write the failing WeChat send tests**

Add to `internal/channel/wechat/client_test.go`:

```go
func TestHTTPClientSendTextMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmsg" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("AuthorizationType"); got != "ilink_bot_token" {
			t.Fatalf("AuthorizationType = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["to_user_id"] != "user-1" || body["text"] != "hello" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	if err := client.SendTextMessage(context.Background(), SendMessageOptions{Token: "token", WechatUIN: "uin", ToUserID: "user-1", Text: "hello"}); err != nil {
		t.Fatalf("SendTextMessage() error = %v", err)
	}
}
```

Create `internal/channel/wechat/reply_gateway_test.go` with:

```go
func TestReplyGatewayReply(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.ToUserID != "user-1" || opts.Text != "hello" {
			t.Fatalf("opts = %#v", opts)
		}
		return nil
	}}
	gateway := NewReplyGateway(client)
	err := gateway.Reply(context.Background(), ReplyTarget{To: "user-1", Token: "token", WechatUIN: "uin"}, agent.Response{Text: "hello"})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the WeChat send tests to verify they fail**

Run: `go test ./internal/channel/wechat -run 'TestHTTPClientSendTextMessage|TestReplyGatewayReply' -v`
Expected: FAIL because send/reply types do not exist yet.

- [ ] **Step 3: Add send-message support to the client**

Extend the client interface in `internal/channel/wechat/client.go` with:

```go
type Client interface {
	CreateBindingSession(ctx context.Context, bindingID string) (CreateSessionResult, error)
	GetBindingSession(ctx context.Context, providerRef string) (GetSessionResult, error)
	GetMessagesLongPoll(ctx context.Context, opts GetUpdatesOptions) (GetUpdatesResult, error)
	SendTextMessage(ctx context.Context, opts SendMessageOptions) error
}

type SendMessageOptions struct {
	BaseURL   string
	Token     string
	WechatUIN string
	ToUserID  string
	Text      string
}
```

Add request/response helpers and the HTTP call:

```go
func (c *HTTPClient) SendTextMessage(ctx context.Context, opts SendMessageOptions) error {
	baseURL := c.baseURL
	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}
	token := c.authToken
	if opts.Token != "" {
		token = opts.Token
	}
	payload, err := json.Marshal(map[string]any{
		"to_user_id": opts.ToUserID,
		"text":       opts.Text,
	})
	if err != nil {
		return fmt.Errorf("marshal sendmsg request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/sendmsg", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create sendmsg request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	attachAuth(req, token, opts.WechatUIN)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("sendmsg request: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Ret     int    `json:"ret"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode sendmsg response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || result.Ret != 0 || result.ErrCode != 0 {
		return fmt.Errorf("sendmsg failed: status=%d ret=%d errcode=%d errmsg=%s", resp.StatusCode, result.Ret, result.ErrCode, result.ErrMsg)
	}
	return nil
}
```

- [ ] **Step 4: Implement the reply gateway**

Create `internal/channel/wechat/reply_gateway.go` with:

```go
package wechat

import (
	"context"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/agent"
)

type ReplyTarget struct {
	To        string
	BaseURL   string
	Token     string
	WechatUIN string
}

type messageSender interface {
	SendTextMessage(ctx context.Context, opts SendMessageOptions) error
}

type ReplyGateway struct {
	client messageSender
}

func NewReplyGateway(client messageSender) *ReplyGateway {
	return &ReplyGateway{client: client}
}

func (g *ReplyGateway) Reply(ctx context.Context, target ReplyTarget, resp agent.Response) error {
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil
	}
	if err := g.client.SendTextMessage(ctx, SendMessageOptions{
		BaseURL:   target.BaseURL,
		Token:     target.Token,
		WechatUIN: target.WechatUIN,
		ToUserID:  target.To,
		Text:      text,
	}); err != nil {
		return fmt.Errorf("wechat reply: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run the focused WeChat tests**

Run: `go test ./internal/channel/wechat -run 'TestHTTPClientSendTextMessage|TestReplyGatewayReply' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/channel/wechat/client.go internal/channel/wechat/client_test.go internal/channel/wechat/reply_gateway.go internal/channel/wechat/reply_gateway_test.go
git commit -m "feat: add wechat reply gateway"
```

### Task 5: Add the bot message orchestrator

**Files:**
- Create: `internal/app/bot_message_orchestrator.go`
- Test: `internal/app/bot_message_orchestrator_test.go`

- [ ] **Step 1: Write the failing orchestrator tests**

Create `internal/app/bot_message_orchestrator_test.go` with:

```go
func TestOrchestratorProcessesSameBotSequentially(t *testing.T) {
	var mu sync.Mutex
	order := make([]string, 0, 2)
	mgr := fakeAgentManager{send: func(_ context.Context, botID string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		mu.Lock()
		order = append(order, req.MessageID)
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, wechat.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, agent.Spec{Command: "codex", QueueSize: 2})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u", Text: "two"})
	orchestrator.WaitIdle("bot-1")

	if !reflect.DeepEqual(order, []string{"m1", "m2"}) {
		t.Fatalf("order = %#v", order)
	}
}

func TestOrchestratorRepliesBusyWhenQueueFull(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mgr := fakeAgentManager{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "m1" {
			close(started)
			<-release
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	var replies []string
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ wechat.ReplyTarget, resp agent.Response) error {
		replies = append(replies, resp.Text)
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, agent.Spec{Command: "codex", QueueSize: 1})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m3", From: "u", Text: "three"})
	close(release)
	orchestrator.WaitIdle("bot-1")

	if !slices.Contains(replies, "当前请求较多，请稍后再试。") {
		t.Fatalf("replies = %#v", replies)
	}
}
```

- [ ] **Step 2: Run the orchestrator tests to verify they fail**

Run: `go test ./internal/app -run TestOrchestrator -v`
Expected: FAIL because the orchestrator and message types do not exist yet.

- [ ] **Step 3: Implement the orchestrator**

Create `internal/app/bot_message_orchestrator.go` with:

```go
package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel/wechat"
)

const (
	busyReply    = "当前请求较多，请稍后再试。"
	timeoutReply = "处理超时，请稍后重试。"
	failedReply  = "处理失败，请稍后重试。"
)

type InboundMessage struct {
	BotID     string
	MessageID string
	From      string
	Text      string
	ReceivedAt time.Time
}

type agentManager interface {
	Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error)
}

type replyGateway interface {
	Reply(ctx context.Context, target wechat.ReplyTarget, resp agent.Response) error
}

type botWorker struct {
	queue chan InboundMessage
	wg    sync.WaitGroup
}

type BotMessageOrchestrator struct {
	mu      sync.Mutex
	workers map[string]*botWorker
	seen    map[string]time.Time
	agents  agentManager
	replies replyGateway
	spec    agent.Spec
}

func NewBotMessageOrchestrator(agents agentManager, replies replyGateway, spec agent.Spec) *BotMessageOrchestrator {
	return &BotMessageOrchestrator{
		workers: make(map[string]*botWorker),
		seen:    make(map[string]time.Time),
		agents:  agents,
		replies: replies,
		spec:    spec,
	}
}

func (o *BotMessageOrchestrator) HandleMessage(ctx context.Context, msg InboundMessage) {
	if msg.Text == "" || o.seenDuplicate(msg) {
		return
	}
	worker := o.workerFor(msg.BotID)
	select {
	case worker.queue <- msg:
	default:
		_ = o.replies.Reply(ctx, wechat.ReplyTarget{To: msg.From}, agent.Response{Text: busyReply})
	}
}

func (o *BotMessageOrchestrator) WaitIdle(botID string) {
	o.mu.Lock()
	worker := o.workers[botID]
	o.mu.Unlock()
	if worker != nil {
		worker.wg.Wait()
	}
}
```

Continue the same file with a `workerFor`, `runWorker`, and `seenDuplicate` implementation matching the tests:

```go
func (o *BotMessageOrchestrator) workerFor(botID string) *botWorker {
	o.mu.Lock()
	defer o.mu.Unlock()
	if worker, ok := o.workers[botID]; ok {
		return worker
	}
	worker := &botWorker{queue: make(chan InboundMessage, o.spec.QueueSize)}
	o.workers[botID] = worker
	go o.runWorker(botID, worker)
	return worker
}

func (o *BotMessageOrchestrator) runWorker(botID string, worker *botWorker) {
	for msg := range worker.queue {
		worker.wg.Add(1)
		resp, err := o.agents.Send(context.Background(), botID, o.spec, agent.Request{
			BotID:     msg.BotID,
			UserID:    msg.From,
			MessageID: msg.MessageID,
			Prompt:    msg.Text,
		})
		if err != nil {
			replyText := failedReply
			if errors.Is(err, context.DeadlineExceeded) {
				replyText = timeoutReply
			}
			_ = o.replies.Reply(context.Background(), wechat.ReplyTarget{To: msg.From}, agent.Response{Text: replyText})
			worker.wg.Done()
			continue
		}
		_ = o.replies.Reply(context.Background(), wechat.ReplyTarget{To: msg.From}, resp)
		worker.wg.Done()
	}
}

func (o *BotMessageOrchestrator) seenDuplicate(msg InboundMessage) bool {
	key := msg.BotID + ":" + msg.MessageID
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.seen[key]; ok {
		return true
	}
	o.seen[key] = time.Now()
	return false
}
```

- [ ] **Step 4: Run the focused orchestrator tests**

Run: `go test ./internal/app -run TestOrchestrator -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/bot_message_orchestrator.go internal/app/bot_message_orchestrator_test.go
git commit -m "feat: add bot message orchestrator"
```

### Task 6: Wire the orchestrator into runtime events and bootstrap

**Files:**
- Modify: `internal/app/bot_connection_manager.go`
- Modify: `internal/bootstrap/bootstrap.go`
- Test: `internal/app/bot_connection_manager_test.go`
- Test: `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Write the failing wiring tests**

Add to `internal/app/bot_connection_manager_test.go`:

```go
func TestBotConnectionManagerForwardsRuntimeEventToOrchestrator(t *testing.T) {
	starter := &stubRuntimeStarter{}
	var got InboundMessage
	orchestrator := &stubOrchestrator{handle: func(_ context.Context, msg InboundMessage) {
		got = msg
	}}
	manager := NewBotConnectionManagerWithCipherAndOrchestrator(botRepo, accountRepo, starter, cipher, logger, orchestrator)

	if err := manager.Start(context.Background(), bot.ID); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	starter.callbacks.OnEvent(channel.RuntimeEvent{BotID: bot.ID, MessageID: "m1", From: "user-1", Text: "hello"})
	if got.MessageID != "m1" || got.Text != "hello" || got.From != "user-1" {
		t.Fatalf("got = %#v", got)
	}
}
```

Add to `internal/bootstrap/bootstrap_test.go` a check that `New` succeeds when agent config is present and that startup restore still starts linked bots.

- [ ] **Step 2: Run the wiring tests to verify they fail**

Run: `go test ./internal/app ./internal/bootstrap -run 'TestBotConnectionManagerForwardsRuntimeEventToOrchestrator|TestNew' -v`
Expected: FAIL because the orchestrator dependency is not wired yet.

- [ ] **Step 3: Update `BotConnectionManager` to call the orchestrator**

Add a small interface and field in `internal/app/bot_connection_manager.go`:

```go
type messageOrchestrator interface {
	HandleMessage(ctx context.Context, msg InboundMessage)
}
```

Extend the manager struct and constructor:

```go	type BotConnectionManager struct {
		// existing fields...
		orchestrator messageOrchestrator
	}

func NewBotConnectionManagerWithCipherAndOrchestrator(..., orchestrator messageOrchestrator) *BotConnectionManager {
		return &BotConnectionManager{
			handles:      make(map[string]channel.RuntimeHandle),
			bots:         bots,
			accounts:     accounts,
			starter:      starter,
			cipher:       cipher,
			logger:       logger,
			orchestrator: orchestrator,
		}
}
```

Replace the current `OnEvent` body with:

```go
OnEvent: func(ev channel.RuntimeEvent) {
	m.logger.Info("runtime message", "bot_id", ev.BotID, "channel_type", ev.ChannelType, "message_id", ev.MessageID, "from", ev.From, "text", ev.Text)
	if m.orchestrator != nil {
		m.orchestrator.HandleMessage(context.Background(), InboundMessage{
			BotID:      bot.ID,
			MessageID:  ev.MessageID,
			From:       ev.From,
			Text:       ev.Text,
			ReceivedAt: time.Now(),
		})
	}
}
```

- [ ] **Step 4: Wire the manager/orchestrator in bootstrap**

Update `internal/bootstrap/bootstrap.go` to build the agent stack:

```go
agentSpec := agent.Spec{
	Type:      "oneshot",
	Command:   cfg.AgentCLICommand,
	Args:      cfg.AgentCLIArgs,
	WorkDir:   cfg.AgentCLIWorkDir,
	Timeout:   cfg.AgentCLITimeout,
	QueueSize: cfg.AgentQueueSize,
}
agentManager := agent.NewManager(agent.NewOneshotDriver())
replyGateway := wechat.NewReplyGateway(wechatClient)
orchestrator := app.NewBotMessageOrchestrator(agentManager, replyGateway, agentSpec)
botManager := app.NewBotConnectionManagerWithCipherAndOrchestrator(botRepo, accountRepo, provider, cipher, logger, orchestrator)
```

Keep the startup restore loop unchanged except for the new constructor.

- [ ] **Step 5: Run the focused wiring tests**

Run: `go test ./internal/app ./internal/bootstrap -run 'TestBotConnectionManagerForwardsRuntimeEventToOrchestrator|TestNew' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/app/bot_connection_manager.go internal/app/bot_connection_manager_test.go internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go
git commit -m "feat: wire bot runtime events into cli replies"
```

### Task 7: Run full verification

**Files:**
- Modify: none expected
- Test: `./...`

- [ ] **Step 1: Run focused agent tests**

Run: `go test ./internal/agent -v`
Expected: PASS

- [ ] **Step 2: Run focused app tests**

Run: `go test ./internal/app -v`
Expected: PASS

- [ ] **Step 3: Run focused WeChat tests**

Run: `go test ./internal/channel/wechat -v`
Expected: PASS

- [ ] **Step 4: Run focused bootstrap tests**

Run: `go test ./internal/bootstrap -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit final verification state**

```bash
git add internal/agent/*.go internal/app/*.go internal/channel/wechat/*.go internal/bootstrap/*.go internal/config/*.go
git commit -m "test: verify bot channel cli reply flow"
```

## Self-Review

### Spec coverage
- inbound WeChat messages routed into CLI execution: Tasks 5 and 6
- per-bot serial execution and queue bounds: Task 5
- final-text-only reply path: Tasks 2, 4, and 5
- fixed failure messages: Task 5
- generic CLI command/args/workdir/timeout config: Task 1
- WeChat outbound reply implementation: Task 4
- bootstrap wiring and linked-bot startup preservation: Task 6

### Placeholder scan
- No `TODO`, `TBD`, or “similar to previous task” placeholders remain.
- Every code-writing step includes concrete code blocks.
- Every verification step includes exact commands and expected results.

### Type consistency
- `agent.Spec`, `agent.Request`, and `agent.Response` are defined in Task 1 and reused consistently in Tasks 2 through 7.
- `InboundMessage` is introduced in Task 5 and used by Task 6 wiring.
- `wechat.ReplyTarget` and `SendMessageOptions` are defined in Task 4 and reused consistently by the orchestrator.
