# Codex PTY Driver Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a PTY-backed Codex driver that initializes one interactive Codex terminal per bot session and executes requests by writing to that PTY and reading back the completed response.

**Architecture:** Implement a Codex-specific `agent.Driver` in `internal/agent/codex/driver_pty.go` that registers itself as `codex-pty`, starts Codex under PTY during `Init`, and returns a `SessionRuntime` that serializes `Run` calls. Use helper functions for ANSI stripping, prompt detection, output slicing, and completion waiting so the risky terminal-boundary logic is testable in isolation.

**Tech Stack:** Go 1.23, `github.com/creack/pty`, standard library `os/exec`, `context`, `sync`, existing `internal/agent` registry/session lifecycle, go test

---

## File Map

| File | Responsibility |
|---|---|
| `internal/agent/codex/driver_pty.go` | Codex PTY driver, runtime state machine, PTY startup, per-request execution, helper functions |
| `internal/agent/codex/driver_pty_test.go` | Unit + integration-style tests for PTY runtime and completion detection |
| `internal/agent/driver.go` | Reuse existing registry; no new logic expected |
| `internal/agent/session_test.go` | Optional regression guard that `codex-pty` still obeys session serialization if needed |
| `go.mod` / `go.sum` | Add `github.com/creack/pty` dependency |

## Task 1: Add parsing and completion helpers

**Files:**
- Create: `internal/agent/codex/driver_pty.go`
- Test: `internal/agent/codex/driver_pty_test.go`

- [ ] **Step 1: Write the failing helper tests**

Create `internal/agent/codex/driver_pty_test.go` with focused parsing tests:

```go
func TestStripANSI(t *testing.T) {
	got := stripANSI("\x1b[31mhello\x1b[0m\r\n")
	if got != "hello\n" {
		t.Fatalf("stripANSI() = %q", got)
	}
}

func TestNormalizeOutput(t *testing.T) {
	got := normalizeOutput("a\r\nb\rc")
	if got != "a\nb\nc" {
		t.Fatalf("normalizeOutput() = %q", got)
	}
}

func TestFindMarker(t *testing.T) {
	idx := findMarker("before __MYCLAW_END_123__ after", "__MYCLAW_END_123__")
	if idx < 0 {
		t.Fatal("expected marker index")
	}
}

func TestSliceRunOutput(t *testing.T) {
	out, err := sliceRunOutput("prefix\nanswer\n__MYCLAW_END_1__\ncodex> ", len("prefix\n"), "__MYCLAW_END_1__")
	if err != nil {
		t.Fatalf("sliceRunOutput() error = %v", err)
	}
	if out != "answer\n" {
		t.Fatalf("sliceRunOutput() = %q", out)
	}
}
```

- [ ] **Step 2: Run the focused helper tests to verify they fail**

Run: `go test ./internal/agent/codex -run 'Test(StripANSI|NormalizeOutput|FindMarker|SliceRunOutput)$' -v`
Expected: FAIL because the helper functions and file do not exist yet.

- [ ] **Step 3: Write the minimal helper implementations**

Create the initial `internal/agent/codex/driver_pty.go` with helper functions only:

```go
package codex

import (
	"fmt"
	"regexp"
	"strings"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}

func normalizeOutput(text string) string {
	text = stripANSI(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func findMarker(text, marker string) int {
	return strings.Index(text, marker)
}

func sliceRunOutput(text string, start int, marker string) (string, error) {
	if start < 0 || start > len(text) {
		return "", fmt.Errorf("invalid start offset: %d", start)
	}
	segment := text[start:]
	idx := strings.Index(segment, marker)
	if idx < 0 {
		return "", fmt.Errorf("marker not found")
	}
	return segment[:idx], nil
}
```

- [ ] **Step 4: Run the helper tests**

Run: `go test ./internal/agent/codex -run 'Test(StripANSI|NormalizeOutput|FindMarker|SliceRunOutput)$' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/driver_pty.go internal/agent/codex/driver_pty_test.go
git commit -m "feat: add codex pty parsing helpers"
```

## Task 2: Add driver registration and Init-time PTY startup

**Files:**
- Modify: `internal/agent/codex/driver_pty.go`
- Test: `internal/agent/codex/driver_pty_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing Init tests**

Add these tests to `internal/agent/codex/driver_pty_test.go`:

```go
func TestPTYDriverRegistersCodexPTY(t *testing.T) {
	driver, ok := agent.LookupDriver("codex-pty")
	if !ok {
		t.Fatal("expected codex-pty driver registration")
	}
	if driver == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestPTYDriverInitRejectsEmptyCommand(t *testing.T) {
	driver := NewPTYDriver()
	_, err := driver.Init(context.Background(), agent.Spec{Type: "codex-pty"})
	if err == nil {
		t.Fatal("expected empty command error")
	}
}
```

Add a startup test using a fake terminal helper command:

```go
func TestPTYDriverInitStartsReadyRuntime(t *testing.T) {
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "ready-only"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if runtime == nil {
		t.Fatal("expected runtime")
	}
}
```

- [ ] **Step 2: Run the focused Init tests to verify they fail**

Run: `go test ./internal/agent/codex -run 'TestPTYDriver(RegistersCodexPTY|InitRejectsEmptyCommand|InitStartsReadyRuntime)$' -v`
Expected: FAIL because driver registration and PTY startup do not exist yet.

- [ ] **Step 3: Add the PTY dependency and driver skeleton**

Update `go.mod` by adding:

```go
require github.com/creack/pty v1.1.21
```

Then extend `internal/agent/codex/driver_pty.go` with the driver/runtime shell:

```go
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/benenen/myclaw/internal/agent"
)

type PTYDriver struct{}

type runtimeState string

const (
	stateStarting runtimeState = "starting"
	stateReady    runtimeState = "ready"
	stateRunning  runtimeState = "running"
	stateBroken   runtimeState = "broken"
)

type PTYRuntime struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	ptyFile  *os.File
	state    runtimeState
	prompt   string
	notifyCh chan struct{}
	readErr  error
	raw      []byte
	normalized strings.Builder
}

func init() {
	agent.MustRegisterDriver("codex-pty", func() agent.Driver {
		return NewPTYDriver()
	})
}

func NewPTYDriver() *PTYDriver {
	return &PTYDriver{}
}

func (d *PTYDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("codex pty driver requires command")
	}
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	if env := flattenEnv(spec.Env); len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	file, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	runtime := &PTYRuntime{
		cmd:      cmd,
		ptyFile:  file,
		state:    stateStarting,
		notifyCh: make(chan struct{}, 1),
	}
	go runtime.readLoop()
	if err := runtime.waitReady(ctx, spec.Timeout); err != nil {
		_ = file.Close()
		_ = cmd.Process.Kill()
		return nil, err
	}
	return runtime, nil
}
```

Also add `flattenEnv` locally if needed.

- [ ] **Step 4: Add a fake helper process for ready state**

In `internal/agent/codex/driver_pty_test.go`, add:

```go
func TestHelperProcessCodexPTY(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "ready-only":
		fmt.Print("codex> ")
		select {}
	default:
		os.Exit(2)
	}
}
```

Update the Init test to set `GO_WANT_HELPER_PROCESS=1` through `Spec.Env`.

- [ ] **Step 5: Run the Init tests**

Run: `go test ./internal/agent/codex -run 'TestPTYDriver(RegistersCodexPTY|InitRejectsEmptyCommand|InitStartsReadyRuntime)$' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/agent/codex/driver_pty.go internal/agent/codex/driver_pty_test.go
git commit -m "feat: initialize codex pty runtime"
```

## Task 3: Implement ready detection and the reader loop

**Files:**
- Modify: `internal/agent/codex/driver_pty.go`
- Test: `internal/agent/codex/driver_pty_test.go`

- [ ] **Step 1: Write the failing ready-detection tests**

Add tests:

```go
func TestWaitReadyDetectsPrompt(t *testing.T) {
	runtime := &PTYRuntime{state: stateStarting, notifyCh: make(chan struct{}, 1)}
	runtime.normalized.WriteString("welcome\ncodex> ")
	if err := runtime.waitReady(context.Background(), time.Second); err != nil {
		t.Fatalf("waitReady() error = %v", err)
	}
}

func TestWaitReadyTimesOutWithoutPrompt(t *testing.T) {
	runtime := &PTYRuntime{state: stateStarting, notifyCh: make(chan struct{}, 1)}
	err := runtime.waitReady(context.Background(), 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
```

- [ ] **Step 2: Run the focused ready tests to verify they fail**

Run: `go test ./internal/agent/codex -run 'TestWaitReady(DetectsPrompt|TimesOutWithoutPrompt)$' -v`
Expected: FAIL until `waitReady` logic exists.

- [ ] **Step 3: Implement `readLoop`, prompt detection, and `waitReady`**

Add logic similar to:

```go
func (r *PTYRuntime) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := r.ptyFile.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			r.mu.Lock()
			r.raw = append(r.raw, buf[:n]...)
			r.normalized.WriteString(normalizeOutput(chunk))
			r.mu.Unlock()
			select {
			case r.notifyCh <- struct{}{}:
			default:
			}
		}
		if err != nil {
			r.mu.Lock()
			r.readErr = err
			r.state = stateBroken
			r.mu.Unlock()
			select {
			case r.notifyCh <- struct{}{}:
			default:
			}
			return
		}
	}
}

func hasPrompt(text string) (string, bool) {
	for _, candidate := range []string{"codex> ", "codex❯ ", "> "} {
		if strings.Contains(text, candidate) {
			return candidate, true
		}
	}
	return "", false
}

func (r *PTYRuntime) waitReady(ctx context.Context, timeout time.Duration) error {
	readyCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		readyCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	for {
		r.mu.Lock()
		text := r.normalized.String()
		if prompt, ok := hasPrompt(text); ok {
			r.prompt = prompt
			r.state = stateReady
			r.mu.Unlock()
			return nil
		}
		err := r.readErr
		r.mu.Unlock()
		if err != nil {
			return err
		}
		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-r.notifyCh:
		}
	}
}
```

- [ ] **Step 4: Run the ready tests**

Run: `go test ./internal/agent/codex -run 'TestWaitReady(DetectsPrompt|TimesOutWithoutPrompt)$' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/driver_pty.go internal/agent/codex/driver_pty_test.go
git commit -m "feat: detect codex pty ready state"
```

## Task 4: Implement single-request Run success path

**Files:**
- Modify: `internal/agent/codex/driver_pty.go`
- Test: `internal/agent/codex/driver_pty_test.go`

- [ ] **Step 1: Write the failing Run success test**

Extend helper process support:

```go
case "single-run":
	fmt.Print("codex> ")
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "USER:") {
			fmt.Printf("answer for %s\n__MYCLAW_END_1__\ncodex> ", strings.TrimPrefix(line, "USER:"))
		}
	}
```

Add test:

```go
func TestPTYRuntimeRunReturnsSingleResponse(t *testing.T) {
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "single-run"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(resp.Text, "answer for hello") {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
}
```

- [ ] **Step 2: Run the focused Run test to verify it fails**

Run: `go test ./internal/agent/codex -run TestPTYRuntimeRunReturnsSingleResponse -v`
Expected: FAIL because `Run` is not implemented.

- [ ] **Step 3: Implement `Run` success path**

Add to `driver_pty.go`:

```go
func (r *PTYRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	r.mu.Lock()
	if r.state != stateReady {
		r.mu.Unlock()
		return agent.Response{}, fmt.Errorf("runtime not ready")
	}
	r.state = stateRunning
	start := len(r.normalized.String())
	r.mu.Unlock()

	marker := "__MYCLAW_END_1__"
	payload := "USER:" + req.Prompt + "\n"
	if _, err := r.ptyFile.Write([]byte(payload)); err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	text, err := r.waitForRunCompletion(ctx, marker, start)
	if err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	r.mu.Lock()
	r.state = stateReady
	r.mu.Unlock()
	return agent.Response{Text: strings.TrimSpace(text), RawOutput: text}, nil
}
```

Add helpers:
- `waitForRunCompletion`
- `markBroken`

For the first version, `waitForRunCompletion` can:
- wait for marker in normalized output after `start`
- then slice output with `sliceRunOutput`
- require prompt visible after marker

- [ ] **Step 4: Run the focused Run test**

Run: `go test ./internal/agent/codex -run TestPTYRuntimeRunReturnsSingleResponse -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/driver_pty.go internal/agent/codex/driver_pty_test.go
git commit -m "feat: run codex requests through pty runtime"
```

## Task 5: Add timeout, broken-state, and serial execution coverage

**Files:**
- Modify: `internal/agent/codex/driver_pty.go`
- Test: `internal/agent/codex/driver_pty_test.go`

- [ ] **Step 1: Write the failing robustness tests**

Add tests:

```go
func TestPTYRuntimeRunTimeoutMarksBroken(t *testing.T) {
	// helper mode prints ready prompt but never marker
}

func TestPTYRuntimeRunEOFMarksBroken(t *testing.T) {
	// helper mode exits after receiving input
}

func TestPTYRuntimeSerializesConcurrentRuns(t *testing.T) {
	// run two requests concurrently and assert helper sees them in order
}
```

Use helper modes such as:
- `never-finishes`
- `exit-on-input`
- `ordered-runs`

- [ ] **Step 2: Run the focused robustness tests to verify they fail**

Run: `go test ./internal/agent/codex -run 'TestPTYRuntime(RunTimeoutMarksBroken|RunEOFMarksBroken|SerializesConcurrentRuns)$' -v`
Expected: FAIL until timeout/broken/serialization handling is fully implemented.

- [ ] **Step 3: Implement timeout and broken-state handling**

Update `Run`/helpers so that:
- request timeout returns `context.DeadlineExceeded` or wrapped timeout error
- PTY EOF/read error marks runtime broken
- after any run failure, future `Run` returns a broken-state error
- `Run` is serialized with the runtime mutex across the whole request lifecycle

Suggested pattern:

```go
func (r *PTYRuntime) markBroken(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = stateBroken
	if r.readErr == nil {
		r.readErr = err
	}
}
```

Also update `waitForRunCompletion` to use a request timeout derived from `ctx` plus `spec.Timeout` captured in runtime if needed.

- [ ] **Step 4: Run the robustness tests**

Run: `go test ./internal/agent/codex -run 'TestPTYRuntime(RunTimeoutMarksBroken|RunEOFMarksBroken|SerializesConcurrentRuns)$' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex/driver_pty.go internal/agent/codex/driver_pty_test.go
git commit -m "fix: harden codex pty runtime boundaries"
```

## Task 6: Run final verification

**Files:**
- Modify: none expected
- Test: `./internal/agent/codex`, `./internal/agent`, `./...`

- [ ] **Step 1: Run focused codex PTY tests**

Run: `go test ./internal/agent/codex -v`
Expected: PASS

- [ ] **Step 2: Run broader agent tests**

Run: `go test ./internal/agent/... -v`
Expected: PASS

- [ ] **Step 3: Run the full suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4: Commit final verification state**

```bash
git add internal/agent/codex/driver_pty.go internal/agent/codex/driver_pty_test.go go.mod go.sum
git commit -m "test: verify codex pty driver flow"
```

## Self-Review

### Spec coverage
- PTY-backed Codex driver added: Tasks 1-5
- registry-backed `codex-pty` registration: Task 2
- `Init` starts one PTY session per bot: Tasks 2-3
- `Run` sends requests and returns output: Task 4
- timeout/broken-state handling: Task 5
- focused and full verification: Task 6

### Placeholder scan
- No `TODO`, `TBD`, or deferred placeholders remain.
- Each code-changing step contains concrete code or helper snippets.
- Every verification step includes explicit commands and expected results.

### Type consistency
- `PTYDriver` implements `agent.Driver.Init` throughout.
- `PTYRuntime` implements `agent.SessionRuntime.Run` throughout.
- Helper process modes introduced in tests are referenced consistently across Tasks 2-5.
- `codex-pty` is the registry key used consistently in the plan.
