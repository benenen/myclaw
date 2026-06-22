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

func TestLoadRegistry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(p, []byte(`[{"name":"w","description":"weather","endpoint":"http://x/a2a","auth_token":"sek"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := reg.find("w")
	if !ok || s.Endpoint != "http://x/a2a" || s.AuthToken != "sek" {
		t.Fatalf("find: %+v ok=%v", s, ok)
	}
	if _, ok := reg.find("nope"); ok {
		t.Fatal("unexpected find")
	}
}

func TestLoadRegistryEmptyPath(t *testing.T) {
	reg, err := loadRegistry("")
	if err != nil || len(reg.servers) != 0 {
		t.Fatalf("empty path -> empty registry, got %+v err %v", reg, err)
	}
}

func TestLoadRegistryMissingFile(t *testing.T) {
	if _, err := loadRegistry("/no/such/file.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRunListOmitsToken(t *testing.T) {
	reg := Registry{servers: []Server{{Name: "w", Description: "d", Endpoint: "e", AuthToken: "SECRET"}}}
	out := runList(reg)
	if len(out.Servers) != 1 || out.Servers[0].Name != "w" || out.Servers[0].Endpoint != "e" {
		t.Fatalf("bad list: %+v", out)
	}
	// ServerView has no token field — leaking the auth token is a compile error.
}

func TestRunDispatchMessageResult(t *testing.T) {
	var gotAuth, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req a2aRequest
		json.NewDecoder(r.Body).Decode(&req)
		m := req.Params["message"].(map[string]any)
		gotText = m["parts"].([]any)[0].(map[string]any)["text"].(string)
		w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"kind":"message","role":"agent","parts":[{"kind":"text","text":"the answer"}]}}`))
	}))
	defer srv.Close()
	reg := Registry{servers: []Server{{Name: "w", Endpoint: srv.URL, AuthToken: "sek"}}}
	out, err := runDispatch(context.Background(), reg, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"})
	if err != nil || out.Result != "the answer" {
		t.Fatalf("got %+v err %v", out, err)
	}
	if gotAuth != "Bearer sek" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotText != "hi" {
		t.Fatalf("prompt sent = %q", gotText)
	}
}

func TestRunDispatchTaskResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"kind":"task","status":{"message":{"parts":[{"kind":"text","text":"task-answer"}]}}}}`))
	}))
	defer srv.Close()
	reg := Registry{servers: []Server{{Name: "w", Endpoint: srv.URL}}}
	out, err := runDispatch(context.Background(), reg, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"})
	if err != nil || out.Result != "task-answer" {
		t.Fatalf("got %+v err %v", out, err)
	}
}

func TestRunDispatchUnknownAgent(t *testing.T) {
	reg := Registry{servers: []Server{{Name: "w", Endpoint: "http://unused"}}}
	if _, err := runDispatch(context.Background(), reg, newA2AClient(nil), DispatchInput{AgentName: "ghost", Prompt: "hi"}); err == nil {
		t.Fatal("expected no-such-server error")
	}
}

func TestRunDispatchEmptyPrompt(t *testing.T) {
	reg := Registry{servers: []Server{{Name: "w", Endpoint: "http://unused"}}}
	if _, err := runDispatch(context.Background(), reg, newA2AClient(nil), DispatchInput{AgentName: "w"}); err == nil {
		t.Fatal("expected empty-prompt error")
	}
}

func TestRunDispatchJSONRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"code":-32000,"message":"boom"}}`))
	}))
	defer srv.Close()
	reg := Registry{servers: []Server{{Name: "w", Endpoint: srv.URL}}}
	if _, err := runDispatch(context.Background(), reg, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"}); err == nil {
		t.Fatal("expected a2a json-rpc error")
	}
}

func TestRunDispatchNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	reg := Registry{servers: []Server{{Name: "w", Endpoint: srv.URL}}}
	if _, err := runDispatch(context.Background(), reg, newA2AClient(srv.Client()), DispatchInput{AgentName: "w", Prompt: "hi"}); err == nil {
		t.Fatal("expected non-2xx error")
	}
}
