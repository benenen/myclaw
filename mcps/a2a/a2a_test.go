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

func TestDispatchBooDelta(t *testing.T) {
	before := "line1\nline2\n"                                // 2 history lines before
	after := "line1\nline2\necho hello\nhi there\nuser@h:~$ " // prompt echo + answer + shell prompt
	calls := 0
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		calls++
		switch args[0] {
		case "peek":
			if calls == 1 {
				return []byte(before), 0, nil
			}
			return []byte(after), 0, nil
		case "send", "wait":
			return nil, 0, nil
		}
		return nil, 0, nil
	})()
	got, err := dispatchBoo(context.Background(), "build", "echo hello", "30s")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hi there" { // prompt-echo line + trailing shell prompt trimmed
		t.Fatalf("delta = %q, want %q", got, "hi there")
	}
}

func TestDispatchBooSessionMissing(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) { return nil, 3, nil })() // exit 3
	if _, err := dispatchBoo(context.Background(), "ghost", "hi", "5s"); err == nil {
		t.Fatal("expected session-not-running error")
	}
}

func TestDispatchBooTimeoutStillReturns(t *testing.T) {
	calls := 0
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		calls++
		switch args[0] {
		case "peek":
			if calls == 1 {
				return []byte("a\n"), 0, nil
			}
			return []byte("a\npartial output\n"), 0, nil
		case "wait":
			return nil, 4, nil // timeout, non-fatal
		}
		return nil, 0, nil
	})()
	got, err := dispatchBoo(context.Background(), "build", "x", "1s")
	if err != nil {
		t.Fatalf("timeout should not error: %v", err)
	}
	if got != "partial output" {
		t.Fatalf("got %q", got)
	}
}

func TestRunDispatchRoutesBoo(t *testing.T) {
	calls := 0
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		calls++
		if args[0] == "ls" {
			return []byte(`[{"name":"build","title":"t"}]`), 0, nil
		}
		if args[0] == "peek" {
			if calls <= 2 { // ls then first peek
				return []byte("a\n"), 0, nil
			}
			return []byte("a\nprompt\nresult-text\n"), 0, nil
		}
		return nil, 0, nil
	})()
	out, err := runDispatch(context.Background(), []Source{{Kind: "boo"}}, newA2AClient(nil), DispatchInput{AgentName: "build", Prompt: "prompt"})
	if err != nil || out.Result == "" {
		t.Fatalf("boo route: out=%+v err=%v", out, err)
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

func TestBooConfigDir(t *testing.T) {
	t.Setenv("BOO_CONFIG", "/x/conf.toml")
	t.Setenv("XDG_CONFIG_HOME", "/y")
	if d := booConfigDir(); d != "/x" {
		t.Fatalf("BOO_CONFIG dir = %q, want /x", d)
	}
	t.Setenv("BOO_CONFIG", "")
	if d := booConfigDir(); d != "/y/boo" {
		t.Fatalf("XDG dir = %q, want /y/boo", d)
	}
	// Home fallback: both BOO_CONFIG and XDG_CONFIG_HOME unset.
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "boo")
	if d := booConfigDir(); d != want {
		t.Fatalf("home fallback = %q, want %q", d, want)
	}
}

func TestBooSessionCwd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, "boo"), 0o755)
	os.WriteFile(filepath.Join(tmp, "boo", "build.state"), []byte("/home/me/proj\n"), 0o600)

	cwd, ok := booSessionCwd("build")
	if !ok || cwd != "/home/me/proj" {
		t.Fatalf("cwd=%q ok=%v", cwd, ok)
	}
	if _, ok := booSessionCwd("ghost"); ok {
		t.Fatal("missing .state should be !ok")
	}
}

func TestBooCapabilitiesDescription(t *testing.T) {
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "boo.capabilities.json"),
		[]byte(`{"description":"coding agent","skills":["go","testing"]}`), 0o600)
	got, ok := booCapabilitiesDescription(cwd)
	if !ok || got != "coding agent [skills: go, testing]" {
		t.Fatalf("got %q ok=%v", got, ok)
	}

	cwd2 := t.TempDir()
	os.WriteFile(filepath.Join(cwd2, "boo.capabilities.json"), []byte(`{"description":"plain"}`), 0o600)
	if got, ok := booCapabilitiesDescription(cwd2); !ok || got != "plain" {
		t.Fatalf("plain: got %q ok=%v", got, ok)
	}

	if _, ok := booCapabilitiesDescription(t.TempDir()); ok {
		t.Fatal("missing file should be !ok")
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "boo.capabilities.json"), []byte(`{not json`), 0o600)
	if _, ok := booCapabilitiesDescription(bad); ok {
		t.Fatal("invalid json should be !ok")
	}
	// Empty object: present but no description/skills → ("", false).
	empty := t.TempDir()
	os.WriteFile(filepath.Join(empty, "boo.capabilities.json"), []byte(`{}`), 0o600)
	if got, ok := booCapabilitiesDescription(empty); ok || got != "" {
		t.Fatalf("empty object: got %q ok=%v, want (\"\", false)", got, ok)
	}
}

func TestResolveBooEnrichesDescriptionFromCapabilities(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"bash"}]`), 0, nil
	})()
	tmp := t.TempDir()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cwd := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "boo"), 0o755)
	os.WriteFile(filepath.Join(tmp, "boo", "build.state"), []byte(cwd+"\n"), 0o600)
	os.WriteFile(filepath.Join(cwd, "boo.capabilities.json"), []byte(`{"description":"go coder"}`), 0o600)

	got := resolve(context.Background(), []Source{{Kind: "boo"}})
	if len(got) != 1 || got[0].Description != "go coder" {
		t.Fatalf("enriched: %+v", got)
	}
}

func TestResolveBooFallsBackToTitleWhenNoCapabilities(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"the title"}]`), 0, nil
	})()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty boo dir → no .state
	got := resolve(context.Background(), []Source{{Kind: "boo"}})
	if len(got) != 1 || got[0].Description != "the title" {
		t.Fatalf("fallback: %+v", got)
	}
}

func TestBooRosterParsesSessions(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"a build","idle_ms":1200},{"name":"chat","title":"a chat","idle_ms":50}]`), 0, nil
	})()
	got := booRoster(context.Background())
	if len(got) != 2 || got[0].Name != "build" || got[0].Title != "a build" || got[0].IdleMS != 1200 {
		t.Fatalf("roster: %+v", got)
	}
}

func TestBooRosterEmptyOnFailure(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) { return nil, 1, nil })()
	if got := booRoster(context.Background()); len(got) != 0 {
		t.Fatalf("want empty on ls failure, got %+v", got)
	}
	defer stubBoo(func(args ...string) ([]byte, int, error) { return []byte(`{bad`), 0, nil })()
	if got := booRoster(context.Background()); len(got) != 0 {
		t.Fatalf("want empty on bad json, got %+v", got)
	}
}

func TestBooSessionDetailLiveWithCapability(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"a build","idle_ms":7}]`), 0, nil
	})()
	tmp := t.TempDir()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cwd := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "boo"), 0o755)
	os.WriteFile(filepath.Join(tmp, "boo", "build.state"), []byte(cwd+"\n"), 0o600)
	os.WriteFile(filepath.Join(cwd, "boo.capabilities.json"), []byte(`{"description":"go coder"}`), 0o600)

	d, ok := booSessionDetail(context.Background(), "build")
	if !ok || d.Name != "build" || d.Title != "a build" || d.IdleMS != 7 || d.Cwd != cwd || d.Capability != "go coder" {
		t.Fatalf("detail: %+v ok=%v", d, ok)
	}
}

func TestBooSessionDetailUnknownIsNotOk(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"a build"}]`), 0, nil
	})()
	if _, ok := booSessionDetail(context.Background(), "ghost"); ok {
		t.Fatal("unknown session must be !ok")
	}
}

func TestBooSessionDetailLiveNoCapability(t *testing.T) {
	defer stubBoo(func(args ...string) ([]byte, int, error) {
		return []byte(`[{"name":"build","title":"a build"}]`), 0, nil
	})()
	t.Setenv("BOO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty boo dir → no .state
	d, ok := booSessionDetail(context.Background(), "build")
	if !ok || d.Capability != "" || d.Cwd != "" {
		t.Fatalf("detail: %+v ok=%v (want ok, empty cap/cwd)", d, ok)
	}
}
