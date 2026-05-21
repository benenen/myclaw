package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
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
	mgr := NewManager(&stubBotRepo{}, &stubResolver{}, &stubExecutor{})
	w := httptest.NewRecorder()
	r := newTestRequest(nil)

	mgr.HandleHook(w, r, "unknown")
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

	mgr.HandleHook(w, r, "test")
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

	mgr.HandleHook(w, r, "test")
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

	mgr.HandleHook(w, r, "test")
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

	mgr.HandleHook(w, r, "test")
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

	mgr.HandleHook(w, r, "test")
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
