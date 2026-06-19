# Feishu Agent Execution Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a real-time tool-call trace on Feishu as an in-place interactive card (`Message.Patch`) while the agent runs, followed by the answer as a separate message on the existing send path.

**Architecture:** A nil-safe `OnProgress` callback on `agent.Request` (forwarded untouched `Manager.Send → Session.Send → runtime.Run`) lets ACP drivers emit `agent.ProgressEvent`s during a turn. The orchestrator routes them to a `channel.ProgressReporter`; the Feishu implementation creates/patches a throttled trace card via a background flusher goroutine, then finalizes it before the answer is sent. When tracing is off / not Feishu / no creds, `Begin` returns nil, `OnProgress` stays nil, and every path behaves byte-for-byte as today.

**Tech Stack:** Go 1.23, `net/http`, `github.com/larksuite/oapi-sdk-go/v3` (Feishu IM SDK), standard `testing`.

**Spec:** `docs/superpowers/specs/2026-06-19-feishu-agent-execution-trace-design.md`

---

## File Structure

New files:
- `internal/agent/progress.go` — `ProgressEvent`, `TargetFromInput`, `truncate` (shared by all drivers)
- `internal/agent/progress_test.go`
- `internal/channel/progress.go` — `ProgressSession`, `ProgressReporter`, `MultiProgressReporter`
- `internal/channel/progress_test.go`
- `internal/channel/feishu/progress.go` — `feishuProgressReporter`, `traceSession`, `buildProgressCard`, emoji map
- `internal/channel/feishu/progress_test.go`

Modified files:
- `internal/agent/types.go` — add `Request.OnProgress`
- `internal/channel/feishu/types.go` — add `CardParams`, extend `feishuAPI` interface
- `internal/channel/feishu/api.go` — implement `CreateCard` / `PatchCard`
- `internal/channel/feishu/config.go` — add `Trace` flag from `CHANNEL_FEISHU_TRACE`
- `internal/channel/feishu/fake_test.go` — implement new interface methods on the fake
- `internal/agent/claude/driver_acp.go` — `activeProgress` + `tool_use` extraction
- `internal/agent/codex/driver_acp.go` — `activeProgress` + tool/command item extraction (sample-verified)
- `internal/agent/opencode/driver_acp.go` — `activeProgress` + tool_call update extraction (sample-verified)
- `internal/app/bot/message_orchestrator.go` — `progress` field, `SetProgressReporter`, wiring
- `internal/bootstrap/bootstrap.go` — build & inject `multiProgressReporter`

---

## Task 1: `agent.ProgressEvent` + shared target extraction

**Files:**
- Create: `internal/agent/progress.go`
- Create: `internal/agent/progress_test.go`
- Modify: `internal/agent/types.go` (add field to `Request`)

- [ ] **Step 1: Write the failing test**

`internal/agent/progress_test.go`:
```go
package agent

import "testing"

func TestTargetFromInput_PrefersToolKey(t *testing.T) {
	got := TargetFromInput("Bash", map[string]any{"command": "boo ls", "description": "list"})
	if got != "boo ls" {
		t.Fatalf("got %q, want %q", got, "boo ls")
	}
}

func TestTargetFromInput_FilePathTools(t *testing.T) {
	got := TargetFromInput("Read", map[string]any{"file_path": "internal/api.go"})
	if got != "internal/api.go" {
		t.Fatalf("got %q, want %q", got, "internal/api.go")
	}
}

func TestTargetFromInput_FallsBackToFirstStringField(t *testing.T) {
	got := TargetFromInput("Unknown", map[string]any{"zeta": "z", "alpha": "a"})
	if got != "a" { // first by sorted key
		t.Fatalf("got %q, want %q", got, "a")
	}
}

func TestTargetFromInput_TruncatesTo60(t *testing.T) {
	long := ""
	for i := 0; i < 80; i++ {
		long += "x"
	}
	got := TargetFromInput("Bash", map[string]any{"command": long})
	if len([]rune(got)) != 60 {
		t.Fatalf("len = %d, want 60", len([]rune(got)))
	}
}

func TestRequestOnProgressInvokable(t *testing.T) {
	var got ProgressEvent
	req := Request{OnProgress: func(ev ProgressEvent) { got = ev }}
	req.OnProgress(ProgressEvent{Kind: "tool", Tool: "Bash", Target: "boo ls"})
	if got.Tool != "Bash" || got.Target != "boo ls" {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run 'TargetFromInput|RequestOnProgress' -v`
Expected: FAIL — `undefined: TargetFromInput`, `unknown field OnProgress`.

- [ ] **Step 3: Add the `Request.OnProgress` field**

In `internal/agent/types.go`, add the field to `Request` (after `Metadata`):
```go
type Request struct {
	BotID     string
	UserID    string
	MessageID string
	Prompt    string
	WorkDir   string
	Metadata  map[string]string
	// OnProgress, when non-nil, is invoked by the driver as it parses
	// intermediate tool events during a turn. Nil = no tracing (default).
	OnProgress func(ProgressEvent)
}
```

- [ ] **Step 4: Create `internal/agent/progress.go`**

```go
package agent

import "sort"

// ProgressEvent is one piece of intermediate execution surfaced to a channel
// while a turn runs. v1 only emits Kind "tool".
type ProgressEvent struct {
	Kind   string // "tool" (v1); reserved for "thinking" etc.
	Tool   string // canonical tool name, e.g. "Bash", "Read", "WebFetch"
	Target string // already truncated salient target (command / path / url)
}

const maxTargetLen = 60

// targetKeys maps a tool name to the input field that best describes what it
// is acting on. Unlisted tools fall back to the first string field.
var targetKeys = map[string]string{
	"Bash":      "command",
	"Read":      "file_path",
	"Edit":      "file_path",
	"Write":     "file_path",
	"WebFetch":  "url",
	"WebSearch": "query",
	"Grep":      "pattern",
	"Glob":      "pattern",
	"Task":      "description",
}

// TargetFromInput picks the salient target string from a tool's input map and
// truncates it to maxTargetLen runes. Returns "" when no string field exists.
func TargetFromInput(tool string, input map[string]any) string {
	if key, ok := targetKeys[tool]; ok {
		if v, ok := input[key].(string); ok && v != "" {
			return truncate(v, maxTargetLen)
		}
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := input[k].(string); ok && v != "" {
			return truncate(v, maxTargetLen)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TargetFromInput|RequestOnProgress' -v`
Expected: PASS (all 5).

- [ ] **Step 6: Confirm existing agent tests still pass**

Run: `go test ./internal/agent/...`
Expected: PASS (drivers unaffected — `OnProgress` is nil everywhere).

- [ ] **Step 7: Commit**

```bash
git add internal/agent/progress.go internal/agent/progress_test.go internal/agent/types.go
git commit -m "feat(agent): add ProgressEvent and Request.OnProgress with shared target extraction"
```

---

## Task 2: `channel` progress interfaces + multi-router

**Files:**
- Create: `internal/channel/progress.go`
- Create: `internal/channel/progress_test.go`

- [ ] **Step 1: Write the failing test**

`internal/channel/progress_test.go`:
```go
package channel

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
)

type recordingSession struct{ steps int }

func (s *recordingSession) Ack(context.Context) error              { return nil }
func (s *recordingSession) Step(context.Context, agent.ProgressEvent) { s.steps++ }
func (s *recordingSession) Done(context.Context)                   {}
func (s *recordingSession) Fail(context.Context, string)           {}

type stubReporter struct{ sess ProgressSession }

func (r stubReporter) Begin(context.Context, ReplyTarget) ProgressSession { return r.sess }

func TestMultiProgressReporter_RoutesByChannelType(t *testing.T) {
	sess := &recordingSession{}
	m := NewMultiProgressReporter()
	m.Register("feishu", stubReporter{sess: sess})

	got := m.Begin(context.Background(), ReplyTarget{ChannelType: "feishu"})
	if got != ProgressSession(sess) {
		t.Fatalf("expected the feishu session, got %#v", got)
	}
}

func TestMultiProgressReporter_UnknownChannelReturnsNil(t *testing.T) {
	m := NewMultiProgressReporter()
	if got := m.Begin(context.Background(), ReplyTarget{ChannelType: "wechat"}); got != nil {
		t.Fatalf("expected nil for unregistered channel, got %#v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/ -run MultiProgressReporter -v`
Expected: FAIL — `undefined: NewMultiProgressReporter` / `ProgressSession`.

- [ ] **Step 3: Create `internal/channel/progress.go`**

```go
package channel

import (
	"context"

	"github.com/benenen/myclaw/internal/agent"
)

// ProgressSession is one in-flight turn's live trace surface. All methods are
// best-effort: implementations must never block the caller on the network for
// long and must never let a failure propagate to the answer path.
type ProgressSession interface {
	// Ack eagerly renders the initial "processing" card and returns an error if
	// it could not be created, so the caller can fall back to a plain-text ack.
	Ack(ctx context.Context) error
	// Step records one tool event and schedules a throttled card update.
	Step(ctx context.Context, ev agent.ProgressEvent)
	// Done finalizes the card as succeeded and blocks until the final frame is
	// flushed, so the answer message lands after it.
	Done(ctx context.Context)
	// Fail finalizes the card as failed/timed-out and blocks until flushed.
	Fail(ctx context.Context, reason string)
}

// ProgressReporter begins a trace session for a reply target, or returns nil
// when tracing does not apply (wrong channel, disabled, missing creds).
type ProgressReporter interface {
	Begin(ctx context.Context, target ReplyTarget) ProgressSession
}

// MultiProgressReporter routes Begin by ReplyTarget.ChannelType, mirroring
// MultiReplyGateway. An unregistered channel yields a nil session.
type MultiProgressReporter struct {
	reporters map[string]ProgressReporter
}

func NewMultiProgressReporter() *MultiProgressReporter {
	return &MultiProgressReporter{reporters: make(map[string]ProgressReporter)}
}

func (m *MultiProgressReporter) Register(channelType string, r ProgressReporter) {
	m.reporters[channelType] = r
}

func (m *MultiProgressReporter) Begin(ctx context.Context, target ReplyTarget) ProgressSession {
	r, ok := m.reporters[target.ChannelType]
	if !ok {
		return nil
	}
	return r.Begin(ctx, target)
}

var _ ProgressReporter = (*MultiProgressReporter)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/channel/ -run MultiProgressReporter -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/channel/progress.go internal/channel/progress_test.go
git commit -m "feat(channel): add ProgressSession/Reporter and MultiProgressReporter router"
```

---

## Task 3: Feishu progress card rendering (pure)

**Files:**
- Create: `internal/channel/feishu/progress.go` (rendering only this task)
- Create: `internal/channel/feishu/progress_test.go`

- [ ] **Step 1: Write the failing test**

`internal/channel/feishu/progress_test.go`:
```go
package feishu

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func markdownContent(t *testing.T, cardJSON string) string {
	t.Helper()
	var card struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("card is not valid json: %v", err)
	}
	if len(card.Elements) == 0 || card.Elements[0].Tag != "markdown" {
		t.Fatalf("expected a markdown element, got %s", cardJSON)
	}
	return card.Elements[0].Content
}

func TestBuildProgressCard_InProgress(t *testing.T) {
	st := traceState{
		steps: []traceStep{{tool: "Bash", target: "boo ls"}, {tool: "Read", target: "api.go"}},
	}
	md := markdownContent(t, buildProgressCard(st))
	if !strings.Contains(md, "处理中") {
		t.Fatalf("missing in-progress header: %s", md)
	}
	if !strings.Contains(md, "🔧 Bash") || !strings.Contains(md, "boo ls") {
		t.Fatalf("missing bash step: %s", md)
	}
	if !strings.Contains(md, "📖 Read") {
		t.Fatalf("missing read step: %s", md)
	}
}

func TestBuildProgressCard_Done(t *testing.T) {
	st := traceState{
		steps:    []traceStep{{tool: "Bash", target: "boo ls"}},
		terminal: "done",
		elapsed:  26 * time.Second,
	}
	md := markdownContent(t, buildProgressCard(st))
	if !strings.Contains(md, "✅") || !strings.Contains(md, "1 步") || !strings.Contains(md, "26s") {
		t.Fatalf("bad done header: %s", md)
	}
}

func TestBuildProgressCard_Fail(t *testing.T) {
	st := traceState{terminal: "fail", reason: "处理超时"}
	md := markdownContent(t, buildProgressCard(st))
	if !strings.Contains(md, "⚠️") || !strings.Contains(md, "处理超时") {
		t.Fatalf("bad fail header: %s", md)
	}
}

func TestBuildProgressCard_CapsToLast25(t *testing.T) {
	var steps []traceStep
	for i := 0; i < 30; i++ {
		steps = append(steps, traceStep{tool: "Bash", target: "cmd"})
	}
	md := markdownContent(t, buildProgressCard(traceState{steps: steps}))
	if !strings.Contains(md, "+5 步") {
		t.Fatalf("expected overflow marker for 30 steps: %s", md)
	}
	if strings.Count(md, "🔧 Bash") != 25 {
		t.Fatalf("expected 25 rendered steps, got %d", strings.Count(md, "🔧 Bash"))
	}
}

func TestToolEmoji(t *testing.T) {
	cases := map[string]string{"Bash": "🔧", "Read": "📖", "Edit": "✏️", "WebFetch": "🌐", "Mystery": "▸"}
	for tool, want := range cases {
		if got := toolEmoji(tool); got != want {
			t.Fatalf("toolEmoji(%q)=%q want %q", tool, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/feishu/ -run 'BuildProgressCard|ToolEmoji' -v`
Expected: FAIL — `undefined: traceState` / `buildProgressCard` / `toolEmoji`.

- [ ] **Step 3: Create `internal/channel/feishu/progress.go` (rendering only)**

```go
package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxTraceLines = 25

// traceStep is one rendered tool line.
type traceStep struct {
	tool   string
	target string
}

// traceState is the immutable snapshot buildProgressCard renders.
type traceState struct {
	steps    []traceStep
	terminal string // "" in-progress | "done" | "fail"
	reason   string // failure reason when terminal == "fail"
	elapsed  time.Duration
}

var toolEmojis = map[string]string{
	"Bash":      "🔧",
	"Read":      "📖",
	"Edit":      "✏️",
	"Write":     "✏️",
	"Grep":      "🔍",
	"Glob":      "🔍",
	"WebFetch":  "🌐",
	"WebSearch": "🌐",
	"Task":      "🤖",
}

func toolEmoji(tool string) string {
	if e, ok := toolEmojis[tool]; ok {
		return e
	}
	return "▸"
}

func traceHeader(st traceState) string {
	switch st.terminal {
	case "done":
		return fmt.Sprintf("✅ 完成 · %d 步 · %ds", len(st.steps), int(st.elapsed.Seconds()))
	case "fail":
		if strings.TrimSpace(st.reason) != "" {
			return "⚠️ 失败：" + st.reason
		}
		return "⚠️ 失败"
	default:
		return "🤖 处理中…"
	}
}

// buildProgressCard renders the trace as a feishu interactive card. Only the
// last maxTraceLines steps are shown; overflow is summarized at the top.
func buildProgressCard(st traceState) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(traceHeader(st))
	b.WriteString("**")

	steps := st.steps
	if len(steps) > maxTraceLines {
		b.WriteString(fmt.Sprintf("\n…(+%d 步)", len(steps)-maxTraceLines))
		steps = steps[len(steps)-maxTraceLines:]
	}
	for _, s := range steps {
		b.WriteString("\n")
		b.WriteString(toolEmoji(s.tool))
		b.WriteString(" ")
		b.WriteString(s.tool)
		if s.target != "" {
			b.WriteString("  ")
			b.WriteString(s.target)
		}
	}

	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"elements": []any{map[string]any{"tag": "markdown", "content": b.String()}},
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(encoded)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/channel/feishu/ -run 'BuildProgressCard|ToolEmoji' -v`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
git add internal/channel/feishu/progress.go internal/channel/feishu/progress_test.go
git commit -m "feat(feishu): render agent trace as an interactive card"
```

---

## Task 4: Feishu `CreateCard` / `PatchCard` API + `CardParams`

**Files:**
- Modify: `internal/channel/feishu/types.go` (add `CardParams`, extend `feishuAPI`)
- Modify: `internal/channel/feishu/api.go` (implement methods)
- Modify: `internal/channel/feishu/fake_test.go` (implement on fake)

- [ ] **Step 1: Add `CardParams` and extend the interface in `types.go`**

Add the struct near `SendParams`:
```go
// CardParams describes an outbound interactive-card message. A non-empty
// ReplyMessageID threads it under the original message.
type CardParams struct {
	ChatID         string
	Content        string // interactive-card JSON
	ReplyMessageID string
}
```
Extend the `feishuAPI` interface:
```go
type feishuAPI interface {
	ValidateApp(ctx context.Context, appID, appSecret string) (AppInfo, error)
	SendText(ctx context.Context, creds Credentials, p SendParams) error
	// CreateCard sends a new interactive card and returns its message id.
	CreateCard(ctx context.Context, creds Credentials, p CardParams) (string, error)
	// PatchCard replaces the content of an existing interactive card.
	PatchCard(ctx context.Context, creds Credentials, messageID, cardJSON string) error
}
```

- [ ] **Step 2: Run build to verify it fails**

Run: `go build ./internal/channel/feishu/`
Expected: FAIL — `*apiClient` does not implement `feishuAPI` (missing `CreateCard`, `PatchCard`).

- [ ] **Step 3: Implement the methods in `api.go`**

Append to `api.go`:
```go
// CreateCard sends an interactive-card message (threaded when ReplyMessageID is
// set) and returns the new message id for later patching.
func (a *apiClient) CreateCard(ctx context.Context, creds Credentials, p CardParams) (string, error) {
	client := a.larkClient(creds.AppID, creds.AppSecret)
	if p.ReplyMessageID != "" {
		resp, err := client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(p.ReplyMessageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeInteractive).
				Content(p.Content).
				Build()).
			Build())
		if err != nil {
			return "", err
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu reply card failed: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data == nil || resp.Data.MessageId == nil {
			return "", fmt.Errorf("feishu reply card returned no message id")
		}
		return *resp.Data.MessageId, nil
	}

	resp, err := client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.CreateMessageV1ReceiveIDTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(p.ChatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(p.Content).
			Build()).
		Build())
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu create card failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", fmt.Errorf("feishu create card returned no message id")
	}
	return *resp.Data.MessageId, nil
}

// PatchCard replaces the content of an existing interactive card in place.
func (a *apiClient) PatchCard(ctx context.Context, creds Credentials, messageID, cardJSON string) error {
	client := a.larkClient(creds.AppID, creds.AppSecret)
	resp, err := client.Im.V1.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("feishu patch card failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}
```

- [ ] **Step 4: Implement the new methods on the test fake**

In `internal/channel/feishu/fake_test.go`, add to the fake that implements `feishuAPI` (match its existing field/receiver naming; below assumes `fakeAPI`):
```go
func (f *fakeAPI) CreateCard(ctx context.Context, creds Credentials, p CardParams) (string, error) {
	return "msg-1", nil
}

func (f *fakeAPI) PatchCard(ctx context.Context, creds Credentials, messageID, cardJSON string) error {
	return nil
}
```

- [ ] **Step 5: Run build + existing feishu tests**

Run: `go build ./internal/channel/feishu/ && go test ./internal/channel/feishu/`
Expected: build OK; existing tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/channel/feishu/types.go internal/channel/feishu/api.go internal/channel/feishu/fake_test.go
git commit -m "feat(feishu): add CreateCard/PatchCard API for interactive-card updates"
```

---

## Task 5: Feishu `traceSession` + `feishuProgressReporter` (throttle + degraded)

**Files:**
- Modify: `internal/channel/feishu/config.go` (add `Trace`)
- Modify: `internal/channel/feishu/progress.go` (session + reporter)
- Modify: `internal/channel/feishu/progress_test.go` (session tests)

- [ ] **Step 1: Add the `Trace` config flag**

In `internal/channel/feishu/config.go`, extend `Config` and `LoadConfig`:
```go
type Config struct {
	Domain string
	// Trace enables the live tool-call trace card. Default true; disable with
	// CHANNEL_FEISHU_TRACE=0 (or false/off/no).
	Trace bool
}

func LoadConfig() Config {
	return Config{
		Domain: getEnvOrDefault("FEISHU_DOMAIN", "https://open.feishu.cn"),
		Trace:  envBoolDefaultTrue("CHANNEL_FEISHU_TRACE"),
	}
}

func envBoolDefaultTrue(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}
```
Add `"strings"` to the `config.go` import block.

- [ ] **Step 2: Write the failing session tests**

Append to `internal/channel/feishu/progress_test.go`:
```go
import (
	"context"
	"sync"
)

// fakeTraceAPI records CreateCard / PatchCard / SendText calls.
type fakeTraceAPI struct {
	mu          sync.Mutex
	created     int
	patched     []string // card JSON per patch
	sentTexts   []string
	createErr   error
}

func (f *fakeTraceAPI) ValidateApp(context.Context, string, string) (AppInfo, error) { return AppInfo{}, nil }
func (f *fakeTraceAPI) SendText(_ context.Context, _ Credentials, p SendParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentTexts = append(f.sentTexts, p.Text)
	return nil
}
func (f *fakeTraceAPI) CreateCard(context.Context, Credentials, CardParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created++
	return "card-1", nil
}
func (f *fakeTraceAPI) PatchCard(_ context.Context, _ Credentials, _ string, cardJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patched = append(f.patched, cardJSON)
	return nil
}
func (f *fakeTraceAPI) patchCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.patched) }
func (f *fakeTraceAPI) lastPatch() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.patched) == 0 {
		return ""
	}
	return f.patched[len(f.patched)-1]
}

func newTestSession(api feishuAPI) *traceSession {
	return newTraceSession(context.Background(), api, Credentials{AppID: "a", AppSecret: "s"},
		// minInterval 0 => every step flushes immediately for deterministic tests.
		traceTarget{chatID: "c"}, 0)
}

func TestTraceSession_StepsThenDoneSendsFinalFrame(t *testing.T) {
	api := &fakeTraceAPI{}
	s := newTestSession(api)
	s.Step(context.Background(), agentEvent("Bash", "boo ls"))
	s.Step(context.Background(), agentEvent("Read", "api.go"))
	s.Done(context.Background())

	if api.created != 1 {
		t.Fatalf("created=%d want 1", api.created)
	}
	if !strings.Contains(api.lastPatch(), "✅ 完成") || !strings.Contains(api.lastPatch(), "2 步") {
		t.Fatalf("final frame not a done card: %s", api.lastPatch())
	}
}

func TestTraceSession_AckCreatesCardEagerly(t *testing.T) {
	api := &fakeTraceAPI{}
	s := newTestSession(api)
	if err := s.Ack(context.Background()); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if api.created != 1 {
		t.Fatalf("created=%d want 1", api.created)
	}
	s.Fail(context.Background(), "boom")
	if !strings.Contains(api.lastPatch(), "⚠️") || !strings.Contains(api.lastPatch(), "boom") {
		t.Fatalf("final frame not a fail card: %s", api.lastPatch())
	}
}

func TestTraceSession_AckCreateFailureReturnsError(t *testing.T) {
	api := &fakeTraceAPI{createErr: context.DeadlineExceeded}
	s := newTestSession(api)
	if err := s.Ack(context.Background()); err == nil {
		t.Fatal("expected ack error when CreateCard fails")
	}
	// Degraded: subsequent steps/finish must not panic and must not patch.
	s.Step(context.Background(), agentEvent("Bash", "x"))
	s.Done(context.Background())
	if api.patchCount() != 0 {
		t.Fatalf("degraded session should not patch, got %d", api.patchCount())
	}
}

func agentEvent(tool, target string) agent.ProgressEvent {
	return agent.ProgressEvent{Kind: "tool", Tool: tool, Target: target}
}
```
Add the import for `"github.com/benenen/myclaw/internal/agent"` to the test file.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/channel/feishu/ -run TraceSession -v`
Expected: FAIL — `undefined: newTraceSession` / `traceSession` / `traceTarget`.

- [ ] **Step 4: Implement the session + reporter in `progress.go`**

Add to `internal/channel/feishu/progress.go` (imports: add `context`, `sync`, `log/slog`; `agent` and `channel`):
```go
// (extend the import block)
//   "context"
//   "sync"
//   "log/slog"
//   "github.com/benenen/myclaw/internal/agent"
//   "github.com/benenen/myclaw/internal/channel"

const (
	traceMinInterval = 700 * time.Millisecond
	tracePatchTimeout = 5 * time.Second
)

// traceTarget is the destination for one trace card.
type traceTarget struct {
	chatID         string
	replyMessageID string // set in group chats to thread under the original
}

type traceSession struct {
	baseCtx context.Context
	api     feishuAPI
	creds   Credentials
	target  traceTarget

	minInterval time.Duration
	start       time.Time

	mu        sync.Mutex
	steps     []traceStep
	messageID string
	created   bool
	degraded  bool
	terminal  string
	reason    string
	dirty     bool
	lastPatch time.Time

	startOnce sync.Once
	wake      chan struct{}
	closed    chan struct{}
	flushed   chan struct{}
}

func newTraceSession(ctx context.Context, api feishuAPI, creds Credentials, target traceTarget, minInterval time.Duration) *traceSession {
	return &traceSession{
		baseCtx:     context.WithoutCancel(ctx),
		api:         api,
		creds:       creds,
		target:      target,
		minInterval: minInterval,
		start:       time.Now(),
		wake:        make(chan struct{}, 1),
		closed:      make(chan struct{}),
		flushed:     make(chan struct{}),
	}
}

func (s *traceSession) snapshot() traceState {
	st := traceState{terminal: s.terminal, reason: s.reason}
	st.steps = append(st.steps, s.steps...)
	if s.terminal == "done" {
		st.elapsed = time.Since(s.start)
	}
	return st
}

// ensureCard creates the card if not yet created. Caller holds s.mu.
func (s *traceSession) ensureCardLocked() error {
	if s.created || s.degraded {
		return nil
	}
	st := s.snapshot()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.baseCtx, tracePatchTimeout)
	id, err := s.api.CreateCard(ctx, s.creds, CardParams{
		ChatID:         s.target.chatID,
		ReplyMessageID: s.target.replyMessageID,
		Content:        buildProgressCard(st),
	})
	cancel()
	s.mu.Lock()
	if err != nil {
		s.degraded = true
		return err
	}
	s.messageID = id
	s.created = true
	s.lastPatch = time.Now()
	return nil
}

// flushNow renders current state and creates-or-patches the card. Best-effort.
func (s *traceSession) flushNow(final bool) {
	s.mu.Lock()
	if s.degraded {
		s.mu.Unlock()
		return
	}
	if !s.created {
		if err := s.ensureCardLocked(); err != nil {
			slog.Warn("feishu trace card create failed", "error", err)
			s.mu.Unlock()
			return
		}
		if !final {
			s.dirty = false
			s.mu.Unlock()
			return // create already rendered current state
		}
	}
	st := s.snapshot()
	s.dirty = false
	s.lastPatch = time.Now()
	id := s.messageID
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(s.baseCtx, tracePatchTimeout)
	defer cancel()
	if err := s.api.PatchCard(ctx, s.creds, id, buildProgressCard(st)); err != nil {
		slog.Warn("feishu trace card patch failed", "error", err)
	}
}

func (s *traceSession) flushLoop() {
	defer close(s.flushed)
	for {
		select {
		case <-s.closed:
			s.flushNow(true)
			return
		case <-s.wake:
			s.mu.Lock()
			wait := s.minInterval - time.Since(s.lastPatch)
			s.mu.Unlock()
			if wait > 0 {
				select {
				case <-time.After(wait):
				case <-s.closed:
					s.flushNow(true)
					return
				}
			}
			s.flushNow(false)
		}
	}
}

func (s *traceSession) ensureLoop() { s.startOnce.Do(func() { go s.flushLoop() }) }

func (s *traceSession) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Ack synchronously creates the initial card and returns an error on failure.
func (s *traceSession) Ack(ctx context.Context) error {
	s.mu.Lock()
	err := s.ensureCardLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.ensureLoop()
	return nil
}

func (s *traceSession) Step(_ context.Context, ev agent.ProgressEvent) {
	s.mu.Lock()
	s.steps = append(s.steps, traceStep{tool: ev.Tool, target: ev.Target})
	s.dirty = true
	s.mu.Unlock()
	s.ensureLoop()
	s.signal()
}

func (s *traceSession) finish(kind, reason string) {
	s.mu.Lock()
	if s.terminal != "" {
		s.mu.Unlock()
		return
	}
	s.terminal = kind
	s.reason = reason
	started := s.created || len(s.steps) > 0
	s.mu.Unlock()
	if !started {
		return // nothing ever shown (zero-tool turn, no Ack): no card
	}
	s.ensureLoop()
	close(s.closed)
	<-s.flushed
}

func (s *traceSession) Done(context.Context)               { s.finish("done", "") }
func (s *traceSession) Fail(_ context.Context, reason string) { s.finish("fail", reason) }

var _ channel.ProgressSession = (*traceSession)(nil)

// feishuProgressReporter begins trace sessions for feishu targets when tracing
// is enabled and creds resolve.
type feishuProgressReporter struct {
	api      feishuAPI
	registry *Registry
	enabled  bool
}

func NewProgressReporter(api feishuAPI, registry *Registry, enabled bool) *feishuProgressReporter {
	return &feishuProgressReporter{api: api, registry: registry, enabled: enabled}
}

func (r *feishuProgressReporter) Begin(ctx context.Context, target channel.ReplyTarget) channel.ProgressSession {
	if !r.enabled || target.ChannelType != ChannelType {
		return nil
	}
	botID := strings.TrimSpace(target.MetadataValue("bot_id"))
	creds, ok := r.registry.Lookup(botID)
	if !ok {
		return nil
	}
	chatID := strings.TrimSpace(target.MetadataValue("chat_id"))
	if chatID == "" {
		chatID = strings.TrimSpace(target.RecipientID)
	}
	tt := traceTarget{chatID: chatID}
	if target.MetadataValue("chat_type") == "group" {
		tt.replyMessageID = strings.TrimSpace(target.MetadataValue("message_id"))
	}
	return newTraceSession(ctx, r.api, creds, tt, traceMinInterval)
}

var _ channel.ProgressReporter = (*feishuProgressReporter)(nil)
```

> Note: `buildProgressCard` already exists from Task 3. The degraded path means a failed `CreateCard` permanently disables the card for that turn; the orchestrator handles the text-ack fallback via `Ack`'s returned error (Task 6).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/channel/feishu/ -run 'TraceSession|BuildProgressCard|ToolEmoji' -v`
Expected: PASS.

- [ ] **Step 6: Run the whole feishu + config packages**

Run: `go test ./internal/channel/feishu/ ./internal/config/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/channel/feishu/progress.go internal/channel/feishu/progress_test.go internal/channel/feishu/config.go
git commit -m "feat(feishu): throttled trace session + progress reporter with degraded fallback"
```

---

## Task 6: Orchestrator wiring + bootstrap injection

**Files:**
- Modify: `internal/app/bot/message_orchestrator.go`
- Modify: `internal/bootstrap/bootstrap.go`
- Create test: extend `internal/app/bot/message_orchestrator_test.go` (or a new `_test.go` in package `bot`)

- [ ] **Step 1: Write the failing test**

Create `internal/app/bot/message_orchestrator_progress_test.go`. This uses the
existing `fakeExecutor{send}`, `fakeResolver{resolve}`, `fakeReplyGateway{reply}`
fakes and drives the orchestrator via `HandleMessage` (async, like the other
orchestrator tests):
```go
package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type progressCounts struct{ ack, steps, done, failed int }

type fakeProgressSession struct {
	mu     sync.Mutex
	c      progressCounts
	ackErr error
}

func (s *fakeProgressSession) Ack(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.ack++
	return s.ackErr
}
func (s *fakeProgressSession) Step(context.Context, agent.ProgressEvent) { s.mu.Lock(); s.c.steps++; s.mu.Unlock() }
func (s *fakeProgressSession) Done(context.Context)                      { s.mu.Lock(); s.c.done++; s.mu.Unlock() }
func (s *fakeProgressSession) Fail(context.Context, string)              { s.mu.Lock(); s.c.failed++; s.mu.Unlock() }
func (s *fakeProgressSession) counts() progressCounts                    { s.mu.Lock(); defer s.mu.Unlock(); return s.c }

type fakeProgressReporter struct {
	sess    *fakeProgressSession
	nilSess bool
}

func (r *fakeProgressReporter) Begin(context.Context, channel.ReplyTarget) channel.ProgressSession {
	if r.nilSess {
		return nil
	}
	return r.sess
}

func feishuInbound(text string) InboundMessage {
	return InboundMessage{
		BotID: "bot-1", MessageID: "m1", From: "u", Text: text,
		ReplyTarget: channel.ReplyTarget{ChannelType: "feishu", RecipientID: "c"},
	}
}

func waitReply(t *testing.T, ch <-chan agent.Response) agent.Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply")
		return agent.Response{}
	}
}

func TestOrchestrator_PlainBot_WiresProgressOnSuccess(t *testing.T) {
	sess := &fakeProgressSession{}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.OnProgress != nil {
			req.OnProgress(agent.ProgressEvent{Kind: "tool", Tool: "Bash", Target: "boo ls"})
		}
		return agent.Response{Text: "ok"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex"}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))

	if resp := waitReply(t, replied); resp.Text != "ok" {
		t.Fatalf("answer = %q", resp.Text)
	}
	c := sess.counts()
	if c.done != 1 || c.failed != 0 || c.steps != 1 || c.ack != 0 {
		t.Fatalf("counts = %+v, want {ack:0 steps:1 done:1 failed:0}", c)
	}
}

func TestOrchestrator_PlainBot_WiresFailOnError(t *testing.T) {
	sess := &fakeProgressSession{}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{}, context.DeadlineExceeded
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex"}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))
	_ = waitReply(t, replied) // failed/timeout reply

	c := sess.counts()
	if c.failed != 1 || c.done != 0 {
		t.Fatalf("counts = %+v, want failed:1 done:0", c)
	}
}

func TestOrchestrator_OrchestratorBot_AcksViaSession(t *testing.T) {
	sess := &fakeProgressSession{}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{Text: "answer"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", Orchestrator: true}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))
	if resp := waitReply(t, replied); resp.Text != "answer" {
		t.Fatalf("answer = %q (ack must NOT be a separate text reply)", resp.Text)
	}
	c := sess.counts()
	if c.ack != 1 || c.done != 1 {
		t.Fatalf("counts = %+v, want ack:1 done:1", c)
	}
}

func TestOrchestrator_NilReporter_BehavesAsToday(t *testing.T) {
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.OnProgress != nil {
			t.Error("OnProgress must be nil when no session")
		}
		return agent.Response{Text: "ok"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex"}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{nilSess: true})

	o.HandleMessage(context.Background(), feishuInbound("hello")) // must not panic
	if resp := waitReply(t, replied); resp.Text != "ok" {
		t.Fatalf("answer = %q", resp.Text)
	}
}
```

> **Note:** `fakeExecutor`, `fakeResolver`, and `fakeReplyGateway` already exist in
> `internal/app/bot/message_orchestrator_test.go`; reuse them as-is. If any field
> name differs, grep that file and match it — do not redefine the fakes.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/bot/ -run 'Orchestrator_(PlainBot|OrchestratorBot|NilReporter)' -v`
Expected: FAIL — `o.SetProgressReporter` undefined.

- [ ] **Step 3: Add the progress field, interface, and setter**

In `internal/app/bot/message_orchestrator.go`, add a local interface and a field:
```go
// progressReporter begins a per-turn trace session; nil session = no tracing.
type progressReporter interface {
	Begin(ctx context.Context, target channel.ReplyTarget) channel.ProgressSession
}
```
Add to `BotMessageOrchestrator` struct:
```go
	progress progressReporter
```
Add a setter (after `SetMessageContext`):
```go
// SetProgressReporter wires the optional live-trace reporter. nil disables tracing.
func (o *BotMessageOrchestrator) SetProgressReporter(p progressReporter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.progress = p
}

func (o *BotMessageOrchestrator) beginProgress(ctx context.Context, target channel.ReplyTarget) channel.ProgressSession {
	o.mu.Lock()
	p := o.progress
	o.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Begin(ctx, target)
}
```

- [ ] **Step 4: Wire the plain-bot path (`processMessage`)**

Replace the body of `processMessage` from the `spec.Orchestrator` check downward (currently lines ~290–347) so a session is begun, `OnProgress` set, and `Done`/`Fail` called. The full replacement:
```go
	if spec.Orchestrator {
		o.runOrchestratorTurn(botID, msg, spec)
		return
	}

	ctx, cancel := o.processingContext(msg.Ctx, spec.Timeout)
	defer cancel()

	sess := o.beginProgress(ctx, msg.ReplyTarget)

	type sendResult struct {
		resp agent.Response
		err  error
	}

	sendDone := make(chan sendResult, 1)
	go func() {
		req := agent.Request{
			BotID:     msg.BotID,
			UserID:    msg.From,
			MessageID: msg.MessageID,
			Prompt:    msg.Text,
		}
		if sess != nil {
			req.OnProgress = func(ev agent.ProgressEvent) { sess.Step(ctx, ev) }
		}
		resp, err := o.executor.Send(ctx, msg.BotID, spec, req)
		sendDone <- sendResult{resp: resp, err: err}
	}()

	var result sendResult
	select {
	case result = <-sendDone:
	case <-ctx.Done():
		log.Printf("processing timeout: bot_id=%s message_id=%s", msg.BotID, msg.MessageID)
		result.err = ctx.Err()
	}

	if result.err != nil {
		log.Printf("agent send failed: bot_id=%s message_id=%s error=%v", msg.BotID, msg.MessageID, result.err)
		replyText := failedReply
		if errors.Is(result.err, context.DeadlineExceeded) || errors.Is(result.err, context.Canceled) {
			replyText = timeoutReply
		} else if result.resp.RuntimeType != "" && strings.TrimSpace(result.resp.Text) != "" {
			replyText = result.resp.RuntimeType + ": " + strings.TrimSpace(result.resp.Text)
		}
		if sess != nil {
			sess.Fail(ctx, replyText)
		}
		o.replyWithTimeout(ctx, msg, agent.Response{Text: replyText})
		o.finishMessageEventually(msg, false)
		return
	}

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
	if sess != nil {
		sess.Done(ctx)
	}
	o.replyWithTimeout(ctx, msg, result.resp)
	o.finishMessageEventually(msg, true)
```

- [ ] **Step 5: Wire the orchestrator-bot path (`runOrchestratorTurn`)**

Replace `runOrchestratorTurn` so the trace card replaces the text ack (falling back to text on Ack error), and `Done`/`Fail` are called in the detached goroutine:
```go
func (o *BotMessageOrchestrator) runOrchestratorTurn(botID string, msg InboundMessage, spec agent.Spec) {
	base := context.WithoutCancel(msg.Ctx)
	sess := o.beginProgress(base, msg.ReplyTarget)
	if sess != nil {
		if err := sess.Ack(base); err != nil {
			sess = nil
			o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})
		}
	} else {
		o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})
	}

	timeout := spec.Timeout
	go func() {
		ctx := base
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(base, timeout)
			defer cancel()
		}
		req := agent.Request{
			BotID:     msg.BotID,
			UserID:    msg.From,
			MessageID: msg.MessageID,
			Prompt:    msg.Text,
		}
		if sess != nil {
			req.OnProgress = func(ev agent.ProgressEvent) { sess.Step(ctx, ev) }
		}
		resp, err := o.executor.Send(ctx, msg.BotID, spec, req)
		if err != nil {
			replyText := failedReply
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				replyText = timeoutReply
			}
			if sess != nil {
				sess.Fail(ctx, replyText)
			}
			o.replyWithTimeout(ctx, msg, agent.Response{Text: replyText})
			o.finishMessageEventually(msg, false)
			return
		}
		if o.sessions != nil && strings.TrimSpace(resp.SessionID) != "" && resp.SessionID != spec.ResumeSessionID {
			if err := o.sessions.Upsert(context.WithoutCancel(ctx), domain.BotCLISession{
				BotID:     msg.BotID,
				CLIType:   resp.RuntimeType,
				SessionID: resp.SessionID,
				WorkDir:   spec.WorkDir,
			}); err != nil {
				log.Printf("cli session upsert failed: bot_id=%s cli=%s error=%v", msg.BotID, resp.RuntimeType, err)
			}
		}
		if sess != nil {
			sess.Done(ctx)
		}
		o.replyWithTimeout(ctx, msg, resp)
		o.finishMessageEventually(msg, true)
	}()
}
```

- [ ] **Step 6: Inject the reporter in bootstrap**

In `internal/bootstrap/bootstrap.go`, after the `feishuReplyGateway` line (~89) add:
```go
	feishuCfg := feishu.LoadConfig()
	feishuProgressReporter := feishu.NewProgressReporter(feishuAPI, feishuRegistry, feishuCfg.Trace)
```
Change the `feishuAPI` construction line (~87) to reuse `feishuCfg`:
```go
	feishuAPI := feishu.NewAPI(feishuCfg)
```
(Move the `feishuCfg := feishu.LoadConfig()` above the `feishuAPI :=` line.)
After `multiReplyGateway` is registered (~99) add:
```go
	multiProgressReporter := channel.NewMultiProgressReporter()
	multiProgressReporter.Register("feishu", feishuProgressReporter)
```
After `orchestrator := bot.NewBotMessageOrchestrator(...)` (~110) add:
```go
	orchestrator.SetProgressReporter(multiProgressReporter)
```

- [ ] **Step 7: Run the new + existing orchestrator tests**

Run: `go test ./internal/app/bot/ -run Orchestrator -v`
Expected: PASS (new progress tests + existing orchestrator tests unchanged).

- [ ] **Step 8: Build the whole module**

Run: `go build ./...`
Expected: builds clean (bootstrap wired).

- [ ] **Step 9: Commit**

```bash
git add internal/app/bot/message_orchestrator.go internal/app/bot/message_orchestrator_progress_test.go internal/bootstrap/bootstrap.go
git commit -m "feat(bot): route agent tool-call progress to channel trace sessions"
```

---

## Task 7: Claude driver tool extraction

**Files:**
- Modify: `internal/agent/claude/driver_acp.go`
- Modify: `internal/agent/claude/driver_acp_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/agent/claude/driver_acp_test.go`:
```go
func TestReadLoop_EmitsToolUseProgress(t *testing.T) {
	var got []agent.ProgressEvent
	var mu sync.Mutex
	r := &ACPRuntime{activeProgress: func(ev agent.ProgressEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}}

	line := `{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"working"},` +
		`{"type":"tool_use","name":"Bash","input":{"command":"boo ls"}},` +
		`{"type":"tool_use","name":"Read","input":{"file_path":"internal/api.go"}}]}}`

	r.handleLine(line) // see Step 3

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Tool != "Bash" || got[0].Target != "boo ls" {
		t.Fatalf("event0 = %+v", got[0])
	}
	if got[1].Tool != "Read" || got[1].Target != "internal/api.go" {
		t.Fatalf("event1 = %+v", got[1])
	}
}
```
Ensure the test file imports `"sync"` and `"github.com/benenen/myclaw/internal/agent"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/claude/ -run EmitsToolUseProgress -v`
Expected: FAIL — `r.activeProgress` / `r.handleLine` undefined.

- [ ] **Step 3: Refactor readLoop to a per-line handler and add progress plumbing**

In `internal/agent/claude/driver_acp.go`:

(a) Add the field to `ACPRuntime` (after `activeTurnCh`):
```go
	activeTurnCh   chan acpTurnEvent
	activeProgress func(agent.ProgressEvent)
```

(b) Extend `claudeStreamEvent`:
```go
type claudeStreamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Message   *struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message,omitempty"`
}
```

(c) Extract a `handleLine` method and call it from `readLoop`. Replace the body of the `for scanner.Scan()` loop in `readLoop` with:
```go
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		r.handleLine(line)
	}
```
Add the method:
```go
func (r *ACPRuntime) handleLine(line string) {
	var evt claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return
	}

	if evt.Type == "system" && evt.Subtype == "init" && evt.SessionID != "" {
		r.mu.Lock()
		r.sessionID = evt.SessionID
		r.mu.Unlock()
	}

	if evt.Type == "assistant" && evt.Message != nil {
		for _, blk := range evt.Message.Content {
			if blk.Type != "tool_use" || blk.Name == "" {
				continue
			}
			var input map[string]any
			_ = json.Unmarshal(blk.Input, &input)
			r.dispatchProgress(agent.ProgressEvent{
				Kind:   "tool",
				Tool:   blk.Name,
				Target: agent.TargetFromInput(blk.Name, input),
			})
		}
	}

	if evt.Type == "result" {
		r.dispatchTurnEvent(acpTurnEvent{
			done:    true,
			text:    evt.Result,
			isError: evt.IsError,
		})
	}
}

func (r *ACPRuntime) dispatchProgress(ev agent.ProgressEvent) {
	r.mu.Lock()
	fn := r.activeProgress
	r.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}
```

(d) In `Run`, set/clear `activeProgress` alongside `activeTurnCh`. Where `Run` does `r.activeTurnCh = turnCh` (under `r.mu`), add `r.activeProgress = req.OnProgress`. In the existing deferred cleanup that nils `activeTurnCh`, also add `r.activeProgress = nil`:
```go
	r.mu.Lock()
	// ...existing state/turnCh setup...
	r.activeTurnCh = turnCh
	r.activeProgress = req.OnProgress
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.activeTurnCh == turnCh {
			r.activeTurnCh = nil
			r.activeProgress = nil
		}
		r.mu.Unlock()
	}()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/claude/ -run EmitsToolUseProgress -v`
Expected: PASS.

- [ ] **Step 5: Run all claude driver tests (regression)**

Run: `go test ./internal/agent/claude/`
Expected: PASS — existing turn/result behavior unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/claude/driver_acp.go internal/agent/claude/driver_acp_test.go
git commit -m "feat(claude): emit tool_use events as ProgressEvents during a turn"
```

---

## Task 8: Codex driver tool extraction (sample-verified)

**Files:**
- Modify: `internal/agent/codex/driver_acp.go`
- Modify: `internal/agent/codex/driver_acp_test.go`

> **VERIFY FIRST — do not skip.** The codex ACP `item/started` payload shape for
> *command/tool* items is not yet handled (only `agentMessage` is). Before
> writing the test, capture one real sample so the field names are correct:
> run a codex bot through a turn that executes a shell command and log the raw
> `item/started` line, OR inspect the installed codex ACP. Concretely:
> `grep -rn "item/started\|commandExecution\|fileChange\|mcpToolCall" $(go env GOPATH)/pkg/mod/ 2>/dev/null | head`
> and check any codex protocol fixtures. Fill the `case` values in Step 3 with
> the confirmed `item.type` strings and field names. If the shape differs from
> the assumption below, adjust the test sample and the parser together.

- [ ] **Step 1: Write the failing test (using the confirmed sample)**

Add to `internal/agent/codex/driver_acp_test.go` (adjust the JSON to the captured sample):
```go
func TestHandleItemStarted_EmitsToolProgress(t *testing.T) {
	var got []agent.ProgressEvent
	var mu sync.Mutex
	r := &ACPRuntime{activeProgress: func(ev agent.ProgressEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}}

	// Confirmed shape from captured sample (EDIT to match real codex output):
	params := json.RawMessage(`{"threadId":"","item":{"type":"commandExecution","command":"boo ls"}}`)
	r.handleItemStarted(params)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Tool != "Bash" || got[0].Target != "boo ls" {
		t.Fatalf("got %+v", got)
	}
}
```
Ensure imports include `"encoding/json"`, `"sync"`, `agent`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/codex/ -run EmitsToolProgress -v`
Expected: FAIL — `r.activeProgress` undefined / no tool event emitted.

- [ ] **Step 3: Add progress plumbing + tool extraction**

(a) Add field to `ACPRuntime` (after `activeTurnCh`):
```go
	activeTurnCh   chan acpTurnEvent
	activeProgress func(agent.ProgressEvent)
```

(b) Add `dispatchProgress` (mirror claude):
```go
func (r *ACPRuntime) dispatchProgress(ev agent.ProgressEvent) {
	r.mu.Lock()
	fn := r.activeProgress
	r.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}
```

(c) Broaden `handleItemStarted` to decode tool/command items in addition to the existing `agentMessage` text handling. Replace the function with (EDIT `case` values to the confirmed shape):
```go
func (r *ACPRuntime) handleItemStarted(params json.RawMessage) {
	var payload struct {
		ThreadID string          `json:"threadId"`
		Item     json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}

	var head struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(payload.Item, &head)

	if head.Type == "agentMessage" {
		for _, content := range head.Content {
			if content.Type == "text" && content.Text != "" {
				r.dispatchTurnEvent(payload.ThreadID, acpTurnEvent{Text: content.Text})
			}
		}
		return
	}

	if tool, target, ok := codexToolFromItem(head.Type, payload.Item); ok {
		r.dispatchProgress(agent.ProgressEvent{Kind: "tool", Tool: tool, Target: target})
	}
}

// codexToolFromItem maps a codex item to (canonical tool, target). EDIT the
// type strings / field names to the confirmed sample.
func codexToolFromItem(itemType string, raw json.RawMessage) (string, string, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "", false
	}
	switch itemType {
	case "commandExecution", "command":
		return "Bash", agent.TargetFromInput("Bash", m), true
	case "fileChange", "patch":
		return "Edit", agent.TargetFromInput("Edit", renameKey(m, "path", "file_path")), true
	case "mcpToolCall", "toolCall":
		name, _ := m["name"].(string)
		if name == "" {
			name = "Task"
		}
		return name, agent.TargetFromInput(name, m), true
	default:
		return "", "", false
	}
}

func renameKey(m map[string]any, from, to string) map[string]any {
	if v, ok := m[from]; ok {
		m[to] = v
	}
	return m
}
```

(d) Set/clear `activeProgress` in `Run` alongside `activeTurnCh` (same pattern as claude Task 7 Step 3d).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/codex/ -run EmitsToolProgress -v`
Expected: PASS.

- [ ] **Step 5: Run all codex driver tests (regression)**

Run: `go test ./internal/agent/codex/`
Expected: PASS — `agentMessage` text handling unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/codex/driver_acp.go internal/agent/codex/driver_acp_test.go
git commit -m "feat(codex): emit tool/command item events as ProgressEvents"
```

---

## Task 9: OpenCode driver tool extraction (sample-verified)

**Files:**
- Modify: `internal/agent/opencode/driver_acp.go`
- Modify: `internal/agent/opencode/driver_acp_test.go`

> **VERIFY FIRST — do not skip.** The opencode `session/update` handler decodes
> only `contentItem(s)` text/delta; the tool-call variant shape is unconfirmed
> and this driver uses a non-standard payload shape. Capture one real
> `session/update` line emitted when opencode runs a tool, then fill the struct
> in Step 3. To capture: temporarily log `string(params)` at the top of
> `handleSessionUpdate`, run an opencode bot through a tool-using turn, copy the
> tool line. Adjust the test sample and parser together to the confirmed shape.

- [ ] **Step 1: Write the failing test (using the confirmed sample)**

Add to `internal/agent/opencode/driver_acp_test.go` (EDIT JSON to the captured sample):
```go
func TestHandleSessionUpdate_EmitsToolProgress(t *testing.T) {
	var got []agent.ProgressEvent
	var mu sync.Mutex
	r := &ACPRuntime{activeProgress: func(ev agent.ProgressEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}}

	// Confirmed shape from captured sample (EDIT to match real opencode output):
	params := json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"tool_call","kind":"Bash","rawInput":{"command":"boo ls"}}}`)
	r.handleSessionUpdate(params)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Tool != "Bash" || got[0].Target != "boo ls" {
		t.Fatalf("got %+v", got)
	}
}
```
Ensure imports include `"encoding/json"`, `"sync"`, `agent`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/opencode/ -run EmitsToolProgress -v`
Expected: FAIL — `r.activeProgress` undefined / no tool event.

- [ ] **Step 3: Add progress plumbing + tool_call extraction**

(a) Add field to `ACPRuntime` (after `activePromptCh`):
```go
	activePromptCh chan acpPromptEvent
	activeProgress func(agent.ProgressEvent)
```

(b) Add `dispatchProgress` (mirror claude/codex).

(c) Extend `handleSessionUpdate` to also decode the tool-call variant. Add at the end of the function, before it returns (EDIT field names to confirmed shape):
```go
	var toolUpd struct {
		Update struct {
			SessionUpdate string         `json:"sessionUpdate"`
			Kind          string         `json:"kind"`
			Title         string         `json:"title"`
			RawInput      map[string]any `json:"rawInput"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &toolUpd); err == nil && toolUpd.Update.SessionUpdate == "tool_call" {
		tool := toolUpd.Update.Kind
		if tool == "" {
			tool = toolUpd.Update.Title
		}
		if tool != "" {
			r.dispatchProgress(agent.ProgressEvent{
				Kind:   "tool",
				Tool:   tool,
				Target: agent.TargetFromInput(tool, toolUpd.Update.RawInput),
			})
		}
	}
```

(d) Set/clear `activeProgress` in `Run` alongside `activePromptCh` (same pattern as claude Task 7 Step 3d, using this driver's existing field name).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/opencode/ -run EmitsToolProgress -v`
Expected: PASS.

- [ ] **Step 5: Run all opencode driver tests (regression)**

Run: `go test ./internal/agent/opencode/`
Expected: PASS — content/delta handling unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/opencode/driver_acp.go internal/agent/opencode/driver_acp_test.go
git commit -m "feat(opencode): emit tool_call updates as ProgressEvents"
```

---

## Task 10: Full suite + manual smoke

- [ ] **Step 1: Run the whole test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 2: Build the server binary**

Run: `go build ./cmd/server`
Expected: builds clean.

- [ ] **Step 3: Manual smoke (optional, needs a real Feishu bot)**

With a Feishu bot connected, send it a prompt that triggers tool use (e.g. a
claude bot asked to run `boo ls`). Expected on Feishu: a "🤖 处理中…" card that
fills with 🔧/📖/✏️ step lines, finalizes to "✅ 完成 · N 步 · Ns", then a
separate answer message (markdown card for rich answers). Then set
`CHANNEL_FEISHU_TRACE=0`, restart, and confirm the trace card disappears and the
bot behaves exactly as before (answer only; orchestrator bots still text-ack).

- [ ] **Step 4: Final commit (if any cleanup)**

```bash
git add -A
git commit -m "test: full-suite green for feishu agent execution trace"
```

---

## Self-Review Notes (for the implementer)

- **Spec coverage:** every spec section maps to a task — transport/OnProgress (T1), channel abstraction (T2), card render (T3), card API (T4), throttle+degraded session+config (T5), orchestrator ack-unification + Done/Fail + bootstrap (T6), per-driver extraction (T7–T9), regression + smoke (T10).
- **Degraded fallback:** `Ack` returns an error; the orchestrator (T6 Step 5) falls back to the plain-text ack — this is the spec's "Ack-failure → text-ack fallback".
- **Nil-safety:** when `Begin` returns nil, `OnProgress` is never set, so drivers do zero extra work (verified the request is forwarded untouched). T6's `TestOrchestrator_NilReporter_BehavesAsToday` guards this.
- **Type consistency:** `agent.ProgressEvent` is the single event struct across agent/channel/feishu; `traceState`/`traceStep`/`traceTarget`/`traceSession` names are used consistently in T3 and T5; `feishuAPI` gains `CreateCard`/`PatchCard` in T4 and is consumed in T5.
- **Sample gates:** codex (T8) and opencode (T9) require capturing a real event sample before finalizing field names; claude (T7) uses the well-known stream-json `tool_use` shape.
