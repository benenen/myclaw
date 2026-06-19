package feishu

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
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

func TestBuildProgressCard_FailBlankReason(t *testing.T) {
	md := markdownContent(t, buildProgressCard(traceState{terminal: "fail"}))
	if !strings.Contains(md, "⚠️ 失败") || strings.Contains(md, "：") {
		t.Fatalf("blank-reason fail header should be bare '⚠️ 失败': %s", md)
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

// fakeTraceAPI records CreateCard / PatchCard / SendText calls.
type fakeTraceAPI struct {
	mu        sync.Mutex
	created   int
	patched   []string
	sentTexts []string
	createErr error
}

func (f *fakeTraceAPI) ValidateApp(context.Context, string, string) (AppInfo, error) {
	return AppInfo{}, nil
}
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
	// minInterval 0 => every step flushes immediately for deterministic tests.
	return newTraceSession(context.Background(), api, Credentials{AppID: "a", AppSecret: "s"},
		traceTarget{chatID: "c"}, 0)
}

func agentEvent(tool, target string) agent.ProgressEvent {
	return agent.ProgressEvent{Kind: "tool", Tool: tool, Target: target}
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
