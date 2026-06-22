# a2a-mcp boo backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `mcps/a2a` so a `{"kind":"boo"}` config source auto-registers live `boo` sessions as A2A servers (visible in `a2a_list`, dispatchable via `a2a_dispatch`), with the HTTP path unchanged.

**Architecture:** Refactor the flat registry into a list of `Source`s (by `kind`) + a `resolve()` that expands them into `ResolvedServer`s — http sources pass through; a boo source runs `boo ls --json` and emits one server per session. `a2a_list`/`a2a_dispatch` resolve on every call. Boo dispatch drives the session through an injectable `runBoo` seam: peek-before → send → wait → peek-after → scrollback line-delta + trim.

**Tech Stack:** Go 1.23, `modelcontextprotocol/go-sdk` v0.8.0, `net/http`, `os/exec`.

Spec: `docs/superpowers/specs/2026-06-22-a2a-boo-backend-design.md`. Branch: `feat/a2a-boo-backend` (off `main`). Only `mcps/a2a/**` changes.

---

## File Structure
- Modify `mcps/a2a/a2a.go` — replace `Server`/`Registry` with `Source`/`ResolvedServer`; add `loadSources`, `resolve`, the `runBoo` seam + `booSession`; change `ServerView` (+`Kind`), `runList`, `a2aClient.send` signature, `runDispatch`; add `dispatchBoo` (Task 2).
- Modify `mcps/a2a/a2a_test.go` — update existing tests to the new signatures; add `resolve`/`dispatchBoo` tests.
- Modify `mcps/a2a/main.go` — `loadSources` + resolve-backed tool handlers.

No `go.work`/`Makefile`/`go.mod` change (the `mcp-a2a` target, go.work entry, and go-sdk v0.8.0 already exist).

---

## Task 1: Sources/resolve model + boo auto-registration in `a2a_list`

After this task: `a2a_list` shows http servers AND every live boo session; `a2a_dispatch` works for http and returns a clear "not yet implemented" for boo (filled in Task 2).

**Files:** Modify `mcps/a2a/a2a.go`, `mcps/a2a/a2a_test.go`, `mcps/a2a/main.go`.

- [ ] **Step 1: Update the tests to the new model (write the failing tests)**

Replace the existing `a2a_test.go` content's registry/list/dispatch parts with the new model. Key changes: `loadRegistry`→`loadSources` (returns `[]Source`), `runList` takes `[]ResolvedServer`, `runDispatch` takes `[]Source`, and add `resolve` tests with a stubbed `runBoo`. Full new test file:

```go
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
```
(Keep the Task-2-era `TestRunDispatchTaskResult`/`ArtifactResult`/`JSONRPCError`/`Non2xx` tests too — they call the HTTP `send`/`extractText` directly, OR adapt them to go through `runDispatch` with an http Source. The simplest: keep them but build a `Source{Kind:"http",...}` and call `runDispatch`; the `extractText` logic is unchanged.)

- [ ] **Step 2: Run — expect FAIL/compile errors** (`loadSources`/`resolve`/`Source` undefined; old `Registry` gone): `go test ./mcps/a2a/`

- [ ] **Step 3: Refactor `a2a.go` — replace the registry model**

In `mcps/a2a/a2a.go`: DELETE the old `Server` struct, `Registry` type, `Registry.find`, and `loadRegistry`. Add the new imports (`bytes`, `log`, `os/exec` — keep existing `context`,`encoding/json`,`fmt`,`os`,`net/http`,`crypto/rand`,`encoding/hex`,`strings`,`bytes`). Add:
```go
const (
	kindHTTP = "http"
	kindBoo  = "boo"
)

// Source is one config entry. kind defaults to "http".
type Source struct {
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	AuthToken   string `json:"auth_token,omitempty"`
	WaitTimeout string `json:"wait_timeout,omitempty"` // boo source default for dispatched waits
}

func (s Source) kind() string {
	if s.Kind == "" {
		return kindHTTP
	}
	return s.Kind
}

// ResolvedServer is a dispatchable target after expanding sources.
type ResolvedServer struct {
	Name        string
	Description string
	Kind        string
	Endpoint    string
	AuthToken   string
	Session     string
	WaitTimeout string
}

func loadSources(path string) ([]Source, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read a2a config %q: %w", path, err)
	}
	var sources []Source
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("parse a2a config %q: %w", path, err)
	}
	return sources, nil
}

// runBoo is the single exec seam for the `boo` CLI; tests stub it.
var runBoo = func(ctx context.Context, args ...string) (stdout []byte, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "boo", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return out.Bytes(), -1, err
	}
	return out.Bytes(), 0, nil
}

type booSession struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// resolve expands sources into live servers. http passes through; a boo source
// runs `boo ls --json` and emits one server per session. boo failures are logged
// and skipped (http sources still resolve). Duplicate names are dropped (first wins).
func resolve(ctx context.Context, sources []Source) []ResolvedServer {
	var out []ResolvedServer
	seen := map[string]bool{}
	add := func(rs ResolvedServer) {
		if rs.Name == "" || seen[rs.Name] {
			return
		}
		seen[rs.Name] = true
		out = append(out, rs)
	}
	for _, s := range sources {
		if s.kind() != kindHTTP {
			continue
		}
		add(ResolvedServer{Name: s.Name, Description: s.Description, Kind: kindHTTP, Endpoint: s.Endpoint, AuthToken: s.AuthToken})
	}
	for _, s := range sources {
		if s.kind() != kindBoo {
			continue
		}
		stdout, code, err := runBoo(ctx, "ls", "--json")
		if err != nil || code != 0 {
			log.Printf("a2a: boo ls failed (skipping boo sessions): code=%d err=%v", code, err)
			continue
		}
		var sessions []booSession
		if len(bytes.TrimSpace(stdout)) > 0 {
			if err := json.Unmarshal(stdout, &sessions); err != nil {
				log.Printf("a2a: parse boo ls --json: %v", err)
				continue
			}
		}
		wt := s.WaitTimeout
		if wt == "" {
			wt = "60s"
		}
		for _, sess := range sessions {
			add(ResolvedServer{Name: sess.Name, Description: sess.Title, Kind: kindBoo, Session: sess.Name, WaitTimeout: wt})
		}
	}
	return out
}
```

- [ ] **Step 4: Update `ServerView`, `runList`, `send`, `runDispatch`**

Change `ServerView` to add `Kind` and `runList` to take resolved servers:
```go
type ServerView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
	Kind        string `json:"kind"`
}

func runList(servers []ResolvedServer) ListOutput {
	views := make([]ServerView, 0, len(servers))
	for _, s := range servers {
		views = append(views, ServerView{Name: s.Name, Description: s.Description, Endpoint: s.Endpoint, Kind: s.Kind})
	}
	return ListOutput{Servers: views}
}
```
Change `a2aClient.send` to take endpoint+token (it no longer references the deleted `Server`):
```go
func (c *a2aClient) send(ctx context.Context, endpoint, authToken, prompt string) (string, error) {
	// ... body unchanged, but use `endpoint` and `authToken` instead of s.Endpoint/s.AuthToken ...
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	// ...
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	// ... rest unchanged ...
}
```
Replace `runDispatch` to resolve + branch (boo is a placeholder error this task):
```go
func runDispatch(ctx context.Context, sources []Source, c *a2aClient, in DispatchInput) (DispatchOutput, error) {
	if in.Prompt == "" {
		return DispatchOutput{}, fmt.Errorf("prompt is required")
	}
	var target *ResolvedServer
	for _, s := range resolve(ctx, sources) {
		if s.Name == in.AgentName {
			s := s
			target = &s
			break
		}
	}
	if target == nil {
		return DispatchOutput{}, fmt.Errorf("no such a2a server: %s", in.AgentName)
	}
	switch target.Kind {
	case kindBoo:
		return DispatchOutput{}, fmt.Errorf("boo dispatch not yet implemented")
	default:
		result, err := c.send(ctx, target.Endpoint, target.AuthToken, in.Prompt)
		if err != nil {
			return DispatchOutput{}, err
		}
		return DispatchOutput{Result: result}, nil
	}
}
```

- [ ] **Step 5: Update `main.go`**

```go
func main() {
	configPath := flag.String("config", os.Getenv("A2A_SERVERS_CONFIG"), "path to the A2A servers JSON config")
	flag.Parse()

	sources, err := loadSources(*configPath)
	if err != nil {
		log.Printf("a2a: config load failed, continuing with no sources: %v", err)
		sources = nil
	}
	client := newA2AClient(http.DefaultClient)

	server := mcp.NewServer(&mcp.Implementation{Name: "a2a", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "a2a_list", Description: "List the A2A servers (incl. live boo sessions) this agent can dispatch subtasks to."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, ListOutput, error) {
			return nil, runList(resolve(ctx, sources)), nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "a2a_dispatch", Description: "Send a self-contained subtask to a named A2A server and return its result."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in DispatchInput) (*mcp.CallToolResult, DispatchOutput, error) {
			out, err := runDispatch(ctx, sources, client, in)
			return nil, out, err
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("a2a mcp server: %v", err)
	}
}
```

- [ ] **Step 6: Run — expect PASS + build**

```bash
cd /root/workspace/master/myclaw/mcps/a2a && go test ./... -v && go vet ./... && go build ./...
cd /root/workspace/master/myclaw && go build ./mcps/... 2>/dev/null; go build ./mcps/echo/... ./mcps/ping/... ./mcps/boo/... ./mcps/a2a/... && go test ./mcps/echo/... ./mcps/ping/... ./mcps/boo/... ./mcps/a2a/...
```
All a2a tests pass (incl. resolve + http dispatch); echo/ping/boo unaffected.

- [ ] **Step 7: Commit**

```bash
cd /root/workspace/master/myclaw
git add mcps/a2a/a2a.go mcps/a2a/a2a_test.go mcps/a2a/main.go
git commit -m "feat(mcps/a2a): source/resolve model + auto-register boo sessions in a2a_list"
```
(Trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.) If a stray `mcps/a2a/a2a` binary appears, `rm` it; do NOT stage it.

---

## Task 2: `dispatchBoo` — drive a session + scrollback delta

**Files:** Modify `mcps/a2a/a2a.go`, `mcps/a2a/a2a_test.go`.

- [ ] **Step 1: Add failing dispatchBoo tests**

```go
func TestDispatchBooDelta(t *testing.T) {
	before := "line1\nline2\n"                                  // 2 history lines before
	after := "line1\nline2\necho hello\nhi there\nuser@h:~$ "   // prompt echo + answer + shell prompt
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
```
Also flip `TestRunDispatchBooNotYetImplemented` (Task 1) into a success case, or replace it: with `dispatchBoo` wired in, dispatching to a boo-kind server should now route through it. Add:
```go
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
```
(Remove the old `TestRunDispatchBooNotYetImplemented`.)

- [ ] **Step 2: Run — expect FAIL** (`dispatchBoo` undefined).

- [ ] **Step 3: Implement `dispatchBoo` + wire the boo branch**

Add to `a2a.go` (imports `strings` already present):
```go
// dispatchBoo types the prompt into a boo session, waits for it to settle, and
// returns the newly-produced scrollback (best-effort: a terminal is not a clean
// request/response channel).
func dispatchBoo(ctx context.Context, session, prompt, waitTimeout string) (string, error) {
	before, err := booPeek(ctx, session)
	if err != nil {
		return "", err
	}
	beforeLines := len(strings.Split(before, "\n"))

	if _, code, err := runBoo(ctx, "send", session, "--text", prompt, "--enter"); err != nil {
		return "", fmt.Errorf("boo not available: %w", err)
	} else if e := booDispatchErr(session, code); e != nil {
		return "", e
	}
	// wait is a settle hint; a timeout (exit 4) is non-fatal.
	if _, _, err := runBoo(ctx, "wait", session, "--idle", "--timeout", waitTimeout); err != nil {
		return "", fmt.Errorf("boo not available: %w", err)
	}

	after, err := booPeek(ctx, session)
	if err != nil {
		return "", err
	}
	afterLines := strings.Split(after, "\n")
	if beforeLines > len(afterLines) {
		beforeLines = len(afterLines)
	}
	delta := afterLines[beforeLines:]
	return trimDelta(delta, prompt), nil
}

func booPeek(ctx context.Context, session string) (string, error) {
	out, code, err := runBoo(ctx, "peek", session, "--scrollback")
	if err != nil {
		return "", fmt.Errorf("boo not available: %w", err)
	}
	if e := booDispatchErr(session, code); e != nil {
		return "", e
	}
	return string(out), nil
}

func booDispatchErr(session string, code int) error {
	switch code {
	case 0, 4: // 4 = wait timeout, handled by caller as non-fatal
		return nil
	case 3:
		return fmt.Errorf("boo session not running: %s", session)
	default:
		return fmt.Errorf("boo error (exit %d) for session %s", code, session)
	}
}

// trimDelta drops a leading prompt-echo line and a trailing shell-prompt line.
func trimDelta(lines []string, prompt string) string {
	// drop leading line if it echoes the prompt
	for len(lines) > 0 && (strings.TrimSpace(lines[0]) == "" || strings.Contains(lines[0], prompt)) {
		lines = lines[1:]
		if strings.Contains(strings.Join(lines[:0], ""), "") { // no-op guard
		}
		break
	}
	if len(lines) > 0 && strings.Contains(lines[0], prompt) {
		lines = lines[1:]
	}
	// drop trailing blank/shell-prompt lines (matches `…$ ` `…# ` `…% ` `…> `)
	for len(lines) > 0 {
		last := strings.TrimRight(lines[len(lines)-1], " ")
		if last == "" || looksLikeShellPrompt(last) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

func looksLikeShellPrompt(s string) bool {
	s = strings.TrimRight(s, " ")
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case '$', '#', '%', '>':
		return !strings.ContainsAny(s, " \t") || strings.HasSuffix(s, "$") || strings.HasSuffix(s, "#") || strings.HasSuffix(s, "%") || strings.HasSuffix(s, ">")
	}
	return false
}
```
> The `trimDelta` above is over-fiddly — IMPLEMENTER: simplify to exactly this behavior and make the three Task-2 tests pass: (a) drop the first delta line if it contains `prompt`; (b) drop trailing lines that are blank or end in `$`/`#`/`%`/`>` after right-trimming spaces; (c) join the rest with `\n`. Keep it readable; the tests are the contract.

Then wire the boo branch in `runDispatch`:
```go
	case kindBoo:
		result, err := dispatchBoo(ctx, target.Session, in.Prompt, target.WaitTimeout)
		if err != nil {
			return DispatchOutput{}, err
		}
		return DispatchOutput{Result: result}, nil
```

- [ ] **Step 4: Run — expect PASS**: `cd mcps/a2a && go test ./... -v && go vet ./... && go build ./...` (all tests incl. the 3 dispatchBoo + the boo-route test). Then the no-blast-radius check (echo/ping/boo).

- [ ] **Step 5: Commit**

```bash
git add mcps/a2a/a2a.go mcps/a2a/a2a_test.go
git commit -m "feat(mcps/a2a): boo session dispatch via scrollback delta"
```
(Trailer as above.)

---

## Task 3: Real-boo smoke

**Files:** none (verification; fix-forward into `mcps/a2a` if the live delta heuristic misbehaves).

- [ ] **Step 1: Build + drive a real boo session through the a2a binary**

```bash
cd /root/workspace/master/myclaw/mcps/a2a && go build -o /tmp/a2a-mcp .
boo new a2asmoke -d -- bash
D=$(mktemp -d); printf '[{"kind":"boo"}]' > "$D/c.json"
# tools/list + a2a_list should now include the a2asmoke session; then a2a_dispatch a prompt.
# Use a minimal MCP JSON-RPC handshake (newline-delimited, NOT LSP framing — go-sdk v0.8.0 StdioTransport):
#   initialize -> notifications/initialized -> tools/call a2a_list  (expect a2asmoke in servers)
#   tools/call a2a_dispatch {agent_name:"a2asmoke", prompt:"echo SMOKE_OK"}  (expect result containing SMOKE_OK)
# If a clean handshake helper isn't available, drive the pieces directly to validate the heuristic:
boo peek a2asmoke --scrollback | wc -l
boo send a2asmoke --text 'echo SMOKE_OK' --enter
boo wait a2asmoke --idle --timeout 5s
boo peek a2asmoke --scrollback     # confirm "SMOKE_OK" appears in the new lines
boo kill a2asmoke
```
Expected: `a2a_list` includes the live session; `a2a_dispatch` to it returns text containing `SMOKE_OK`, with the prompt-echo and trailing shell prompt trimmed. If the real delta/trim is off (e.g. `boo peek --scrollback` line shape differs from the test fixtures), fix `dispatchBoo`/`trimDelta` in `a2a.go`, re-run `go test ./mcps/a2a/...`, and report. Clean up the `a2asmoke` session.

- [ ] **Step 2: Commit any fix-forward** (only if Step 1 required a change)

```bash
git add mcps/a2a/a2a.go mcps/a2a/a2a_test.go
git commit -m "fix(mcps/a2a): align boo scrollback delta with real boo output"
```

---

## Self-Review

**Spec coverage:** sources-by-kind model + `kind` default http + backward compat (Task 1 `Source`/`kind()`/`loadSources` + `TestLoadSources`) ✓; `resolve` http-passthrough + boo `boo ls` expansion + boo-failure-keeps-http (Task 1 + 3 resolve tests) ✓; `a2a_list` auto-registers live sessions (Task 1 main + runList incl Kind) ✓; `a2a_dispatch` resolve+branch, http unchanged (Task 1) ✓; `dispatchBoo` peek-before/send/wait/peek-after line-delta + trim, timeout non-fatal, session-missing error (Task 2 + 3 tests) ✓; `runBoo` seam + stub testing (Task 1) ✓; token never leaked (`ServerView` no token, `TestRunListOmitsTokenIncludesKind`) ✓; real-boo smoke (Task 3) ✓. Out-of-scope (filtering, per-call timeout, marker, streaming) absent.

**Placeholder scan:** Task 2's `trimDelta` block is intentionally followed by an IMPLEMENTER note pinning the exact 3-rule behavior + "the tests are the contract" — the over-fiddly draft must be simplified, not shipped as-is. Task 3's handshake step gives a concrete fallback (drive the boo pieces directly) when no MCP-handshake helper exists. No "TBD"/silent-cap placeholders.

**Type consistency:** `Source{Kind,Name,Description,Endpoint,AuthToken,WaitTimeout}` + `Source.kind()`; `ResolvedServer{Name,Description,Kind,Endpoint,AuthToken,Session,WaitTimeout}`; `resolve(ctx,[]Source)[]ResolvedServer`; `runList([]ResolvedServer)`; `runDispatch(ctx,[]Source,*a2aClient,DispatchInput)`; `(*a2aClient).send(ctx,endpoint,authToken,prompt string)`; `dispatchBoo(ctx,session,prompt,waitTimeout string)`; `runBoo` seam `func(ctx,...string)([]byte,int,error)` — all consistent across a2a.go, the tests, and main.go. The Task-1 placeholder boo error is removed in Task 2.
