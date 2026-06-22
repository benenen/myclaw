package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// stubBoo swaps runBoo for tests; returns a restore func.
func stubBoo(fn func(args ...string) ([]byte, int, error)) func() {
	prev := runBoo
	runBoo = func(_ context.Context, args ...string) ([]byte, int, error) { return fn(args...) }
	return func() { runBoo = prev }
}

func TestLoadSources(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`[{"kind":"http","name":"w","endpoint":"http://x","auth_token":"sek"},{"kind":"boo"},{"name":"noKind","endpoint":"http://y"}]`), 0o600)
	src, err := loadSources(p)
	if err != nil || len(src) != 3 {
		t.Fatalf("sources: %+v err %v", src, err)
	}
	if src[0].kind() != "http" || src[1].kind() != "boo" || src[2].kind() != "http" {
		t.Fatalf("kinds: %q %q %q", src[0].kind(), src[1].kind(), src[2].kind())
	}
}

func TestLoadSourcesEmptyAndMissing(t *testing.T) {
	if s, err := loadSources(""); err != nil || s != nil {
		t.Fatalf("empty path -> nil,nil, got %+v %v", s, err)
	}
	if _, err := loadSources("/no/such.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveHTTPPassthrough(t *testing.T) {
	got := resolve(context.Background(), []Source{{Kind: "http", Name: "w", Description: "d", Endpoint: "e", AuthToken: "t"}})
	if len(got) != 1 || got[0].Kind != "http" || got[0].Endpoint != "e" || got[0].AuthToken != "t" {
		t.Fatalf("resolve http: %+v", got)
	}
}

func TestResolveBooExpandsSessions(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		// expect ["ls","--json"]
		return []byte(`[{"name":"build","title":"a build"},{"name":"chat","title":"a chat"}]`), 0, nil
	})()
	got := resolve(context.Background(), []Source{{Kind: "boo", WaitTimeout: "30s"}})
	if len(got) != 2 {
		t.Fatalf("want 2 boo servers, got %+v", got)
	}
	if got[0].Kind != "boo" || got[0].Name != "build" || got[0].Session != "build" || got[0].Description != "a build" || got[0].WaitTimeout != "30s" {
		t.Fatalf("boo[0]: %+v", got[0])
	}
}

func TestResolveBooFailureKeepsHTTP(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) { return nil, 1, nil })() // boo ls fails
	got := resolve(context.Background(), []Source{{Kind: "http", Name: "w", Endpoint: "e"}, {Kind: "boo"}})
	if len(got) != 1 || got[0].Name != "w" {
		t.Fatalf("boo failure should leave only http, got %+v", got)
	}
}

func TestRunListOmitsTokenIncludesKind(t *testing.T) {
	out := runList([]ResolvedServer{{Name: "w", Description: "d", Endpoint: "e", Kind: "http", AuthToken: "SECRET"}})
	if len(out.Servers) != 1 || out.Servers[0].Kind != "http" || out.Servers[0].Endpoint != "e" {
		t.Fatalf("list: %+v", out)
	}
	// ServerView has no token field — leaking is a compile error.
}

func TestRunDispatchHTTP(t *testing.T) {
	var gotAuth, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req a2aRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotText = req.Params["message"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
		w.Write([]byte(`{"result":{"kind":"message","parts":[{"kind":"text","text":"the answer"}]}}`))
	}))
	defer srv.Close()
	sources := []Source{{Kind: "http", Name: "w", Endpoint: srv.URL, AuthToken: "sek"}}
	out, err := runDispatch(context.Background(), sources, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"})
	if err != nil || out.Result != "the answer" {
		t.Fatalf("got %+v err %v", out, err)
	}
	if gotAuth != "Bearer sek" || gotText != "hi" {
		t.Fatalf("auth=%q text=%q", gotAuth, gotText)
	}
}

func TestRunDispatchUnknownAgent(t *testing.T) {
	if _, err := runDispatch(context.Background(), []Source{{Kind: "http", Name: "w", Endpoint: "http://unused"}}, newA2AClient(nil), DispatchInput{AgentName: "ghost", Prompt: "hi"}); err == nil {
		t.Fatal("expected no-such-server error")
	}
}

func TestRunDispatchEmptyPrompt(t *testing.T) {
	if _, err := runDispatch(context.Background(), []Source{{Kind: "http", Name: "w", Endpoint: "http://unused"}}, newA2AClient(nil), DispatchInput{AgentName: "w"}); err == nil {
		t.Fatal("expected empty-prompt error")
	}
}

func TestRunDispatchBooNotYetImplemented(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"t"}]`), 0, nil
	})()
	if _, err := runDispatch(context.Background(), []Source{{Kind: "boo"}}, newA2AClient(nil), DispatchInput{AgentName: "build", Prompt: "hi"}); err == nil {
		t.Fatal("boo dispatch should error until Task 2")
	}
}

// ---- Task-2-era http result-shape tests (kept; adapted to new []Source signature) ----

func TestRunDispatchTaskResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"kind":"task","status":{"message":{"parts":[{"kind":"text","text":"task-answer"}]}}}}`))
	}))
	defer srv.Close()
	sources := []Source{{Kind: "http", Name: "w", Endpoint: srv.URL}}
	out, err := runDispatch(context.Background(), sources, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"})
	if err != nil || out.Result != "task-answer" {
		t.Fatalf("got %+v err %v", out, err)
	}
}

func TestRunDispatchArtifactResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"kind":"task","artifacts":[{"parts":[{"kind":"text","text":"artifact-answer"}]}]}}`))
	}))
	defer srv.Close()
	sources := []Source{{Kind: "http", Name: "w", Endpoint: srv.URL}}
	out, err := runDispatch(context.Background(), sources, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"})
	if err != nil || out.Result != "artifact-answer" {
		t.Fatalf("got %+v err %v", out, err)
	}
}

func TestRunDispatchJSONRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"code":-32000,"message":"boom"}}`))
	}))
	defer srv.Close()
	sources := []Source{{Kind: "http", Name: "w", Endpoint: srv.URL}}
	if _, err := runDispatch(context.Background(), sources, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"}); err == nil {
		t.Fatal("expected a2a json-rpc error")
	}
}

func TestRunDispatchNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	sources := []Source{{Kind: "http", Name: "w", Endpoint: srv.URL}}
	if _, err := runDispatch(context.Background(), sources, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"}); err == nil {
		t.Fatal("expected non-2xx error")
	}
}
