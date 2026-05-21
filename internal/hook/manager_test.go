package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

// --- test stubs ---

type stubHook struct {
	id     string
	prompt string
	err    error
}

func (h *stubHook) ID() string                                   { return h.id }
func (h *stubHook) Handle(_ context.Context, _ *stdhttp.Request) (string, error) {
	return h.prompt, h.err
}

type stubBotRepo struct {
	bot domain.Bot
	err error
}

func (r *stubBotRepo) GetByName(_ context.Context, name string) (domain.Bot, error) {
	return r.bot, r.err
}

type stubResolver struct {
	spec agent.Spec
	err  error
}

func (r *stubResolver) Resolve(_ context.Context, botID string) (agent.Spec, error) {
	return r.spec, r.err
}

type stubExecutor struct {
	resp agent.Response
	err  error
}

func (e *stubExecutor) Send(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
	return e.resp, e.err
}

// --- helpers ---

func newTestRequest(body any) *stdhttp.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(stdhttp.MethodPost, "/hooks/test", &buf)
	r = r.WithContext(context.Background())
	return r
}

func readEnvelope(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return env
}

// --- tests ---

func TestManager_RegisterHook(t *testing.T) {
	mgr := NewManager(&stubBotRepo{}, &stubResolver{}, &stubExecutor{})
	mgr.RegisterHook(&stubHook{id: "test"})

	if _, ok := mgr.hooks["test"]; !ok {
		t.Fatal("expected hook to be registered")
	}
}

func TestManager_HandleHook_UnknownPlatform(t *testing.T) {
	// No hook registered + no bot found → NOT_FOUND
	mgr := NewManager(&stubBotRepo{err: domain.ErrNotFound}, &stubResolver{}, &stubExecutor{})
	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "unknown", "some-bot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %v", resp["code"])
	}
}

func TestManager_HandleHook_HookValidationError(t *testing.T) {
	mgr := NewManager(&stubBotRepo{}, &stubResolver{}, &stubExecutor{})
	mgr.RegisterHook(&stubHook{id: "test", err: errors.New("bad signature")})

	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "test", "mybot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("expected INVALID_ARGUMENT, got %v", resp["code"])
	}
}

func TestManager_HandleHook_BotNotFound(t *testing.T) {
	mgr := NewManager(&stubBotRepo{err: domain.ErrNotFound}, &stubResolver{}, &stubExecutor{})
	mgr.RegisterHook(&stubHook{id: "test", prompt: "do something"})

	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "test", "mybot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %v", resp["code"])
	}
}

func TestManager_HandleHook_ResolverError(t *testing.T) {
	mgr := NewManager(
		&stubBotRepo{bot: domain.Bot{ID: "bot_1"}},
		&stubResolver{err: errors.New("unsupported mode")},
		&stubExecutor{},
	)
	mgr.RegisterHook(&stubHook{id: "test", prompt: "do something"})

	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "test", "mybot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "INTERNAL_ERROR" {
		t.Fatalf("expected INTERNAL_ERROR, got %v", resp["code"])
	}
}

func TestManager_HandleHook_ExecutorError(t *testing.T) {
	mgr := NewManager(
		&stubBotRepo{bot: domain.Bot{ID: "bot_1"}},
		&stubResolver{spec: agent.Spec{Type: "codex-acp"}},
		&stubExecutor{err: errors.New("agent failed")},
	)
	mgr.RegisterHook(&stubHook{id: "test", prompt: "do something"})

	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "test", "mybot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "INTERNAL_ERROR" {
		t.Fatalf("expected INTERNAL_ERROR, got %v", resp["code"])
	}
}

func TestManager_HandleHook_Success(t *testing.T) {
	mgr := NewManager(
		&stubBotRepo{bot: domain.Bot{ID: "bot_1", Name: "test"}},
		&stubResolver{spec: agent.Spec{Type: "codex-acp"}},
		&stubExecutor{resp: agent.Response{Text: "operation completed"}},
	)
	mgr.RegisterHook(&stubHook{id: "test", prompt: "do something"})

	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "test", "mybot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "OK" {
		t.Fatalf("expected OK, got %v", resp["code"])
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", resp["data"])
	}
	if data["text"] != "operation completed" {
		t.Fatalf("expected text 'operation completed', got %v", data["text"])
	}
}

// --- passthrough tests ---

func TestManager_Passthrough_BotNotFound(t *testing.T) {
	mgr := NewManager(&stubBotRepo{err: domain.ErrNotFound}, &stubResolver{}, &stubExecutor{})
	w := httptest.NewRecorder()
	r := newTestRequest(map[string]string{"event": "test"})

	// No hook registered for "myservice", falls through to passthrough
	mgr.HandleHook(w, r, "myservice", "mybot")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %v", resp["code"])
	}
}

func TestManager_Passthrough_Success(t *testing.T) {
	var receivedPrompt string
	mgr := NewManager(
		&stubBotRepo{bot: domain.Bot{ID: "bot_vikunja", Name: "vikunja"}},
		&stubResolver{spec: agent.Spec{Type: "opencode-acp"}},
		&capturingExecutor{fn: func(req agent.Request) (agent.Response, error) {
			receivedPrompt = req.Prompt
			return agent.Response{Text: "done"}, nil
		}},
	)

	w := httptest.NewRecorder()
	r := newTestRequest(map[string]string{"task": "sync", "data": "xyz"})

	mgr.HandleHook(w, r, "vikunja", "vikunja")
	resp := readEnvelope(t, w.Body.Bytes())
	if resp["code"] != "OK" {
		t.Fatalf("expected OK, got %v", resp["code"])
	}
	if !strings.Contains(receivedPrompt, "task") || !strings.Contains(receivedPrompt, "sync") {
		t.Fatalf("expected passthrough prompt to contain body content, got: %s", receivedPrompt)
	}
}

// capturingExecutor records the prompt sent to it.
type capturingExecutor struct {
	fn func(agent.Request) (agent.Response, error)
}

func (e *capturingExecutor) Send(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
	return e.fn(req)
}
