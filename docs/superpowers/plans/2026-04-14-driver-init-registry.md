# Driver Init Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a registry-backed driver lifecycle where each bot initializes its selected driver once, then reuses that initialized runtime for per-request execution.

**Architecture:** Replace the direct `Driver.Run(spec, req)` contract with a two-stage lifecycle: `Driver.Init(ctx, spec)` creates a session-scoped runtime, and `SessionRuntime.Run(ctx, req)` handles individual requests. Keep bootstrap as the only place choosing `Spec.Type`, and use package-level driver registration via `init()` so future CLIs or model API adapters can plug in without changing orchestrator or manager logic.

**Tech Stack:** Go 1.23, standard library `sync`/`context`, existing `internal/agent` package, go test

---

## File Map

| File | Responsibility |
|---|---|
| `internal/agent/driver.go` | Define the new driver/runtime interfaces and the global registry API |
| `internal/agent/driver_oneshot.go` | Register the oneshot driver and split it into init-time driver + run-time session runtime |
| `internal/agent/session.go` | Create sessions by looking up drivers in the registry and storing initialized runtimes |
| `internal/agent/manager.go` | Manage bot-keyed sessions without owning a concrete driver instance |
| `internal/agent/session_test.go` | Verify init-once, session reuse, recreation on failure/spec change, and runtime execution behavior |
| `internal/agent/driver_oneshot_test.go` | Verify oneshot runtime behavior still matches current execution semantics |
| `internal/bootstrap/bootstrap.go` | Keep selecting the driver type in bootstrap and stop constructing a concrete driver directly |
| `internal/bootstrap/bootstrap_test.go` | Verify bootstrap still builds dependencies after driver-manager construction changes |

## Task 1: Add the driver registry contract

**Files:**
- Modify: `internal/agent/driver.go`
- Test: `internal/agent/session_test.go`

- [ ] **Step 1: Write the failing registry test**

Add to `internal/agent/session_test.go`:

```go
func TestLookupDriverReturnsRegisteredFactory(t *testing.T) {
	const name = "test-registry-driver"
	MustRegisterDriver(name, func() Driver {
		return initStubDriver{}
	})

	driver, ok := LookupDriver(name)
	if !ok {
		t.Fatal("expected registered driver")
	}
	if driver == nil {
		t.Fatal("expected non-nil driver")
	}
}
```

Also add a duplicate-registration panic test:

```go
func TestMustRegisterDriverPanicsOnDuplicate(t *testing.T) {
	const name = "test-duplicate-driver"
	MustRegisterDriver(name, func() Driver { return initStubDriver{} })

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate driver registration")
		}
	}()

	MustRegisterDriver(name, func() Driver { return initStubDriver{} })
}
```

- [ ] **Step 2: Run the focused registry tests to verify they fail**

Run: `go test ./internal/agent -run 'TestLookupDriverReturnsRegisteredFactory|TestMustRegisterDriverPanicsOnDuplicate' -v`
Expected: FAIL because the registry API does not exist yet.

- [ ] **Step 3: Implement the new driver/runtime interfaces and registry**

Replace `internal/agent/driver.go` with:

```go
package agent

import (
	"context"
	"fmt"
	"sync"
)

type Driver interface {
	Init(ctx context.Context, spec Spec) (SessionRuntime, error)
}

type SessionRuntime interface {
	Run(ctx context.Context, req Request) (Response, error)
}

type DriverFactory func() Driver

var (
	driversMu sync.RWMutex
	drivers   = map[string]DriverFactory{}
)

func LookupDriver(name string) (Driver, bool) {
	driversMu.RLock()
	factory, ok := drivers[name]
	driversMu.RUnlock()
	if !ok {
		return nil, false
	}
	return factory(), true
}

func RegisterDriver(name string, factory DriverFactory) error {
	driversMu.Lock()
	defer driversMu.Unlock()
	if _, exists := drivers[name]; exists {
		return fmt.Errorf("driver already registered: %s", name)
	}
	drivers[name] = factory
	return nil
}

func MustRegisterDriver(name string, factory DriverFactory) {
	if err := RegisterDriver(name, factory); err != nil {
		panic(err)
	}
}
```

- [ ] **Step 4: Run the focused registry tests**

Run: `go test ./internal/agent -run 'TestLookupDriverReturnsRegisteredFactory|TestMustRegisterDriverPanicsOnDuplicate' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/driver.go internal/agent/session_test.go
git commit -m "feat: add agent driver registry"
```

## Task 2: Refactor session creation to use Init once per bot

**Files:**
- Modify: `internal/agent/session.go`
- Test: `internal/agent/session_test.go`

- [ ] **Step 1: Write the failing init-once session tests**

Add these test helpers near the top of `internal/agent/session_test.go`:

```go
type initStubDriver struct {
	init func(context.Context, Spec) (SessionRuntime, error)
}

func (d initStubDriver) Init(ctx context.Context, spec Spec) (SessionRuntime, error) {
	return d.init(ctx, spec)
}

type runtimeStub struct {
	run func(context.Context, Request) (Response, error)
}

func (r runtimeStub) Run(ctx context.Context, req Request) (Response, error) {
	return r.run(ctx, req)
}
```

Add a failing test for `Init` happening once per session:

```go
func TestNewSessionInitializesDriverRuntimeOnce(t *testing.T) {
	const name = "test-init-once-driver"
	initCalls := 0
	MustRegisterDriver(name, func() Driver {
		return initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
			initCalls++
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: spec.Command + ":" + req.Prompt}, nil
			}}, nil
		}}
	})

	session, err := NewSession(context.Background(), Spec{Type: name, Command: "codex"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	for _, prompt := range []string{"first", "second"} {
		resp, err := session.Send(context.Background(), Request{Prompt: prompt})
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if resp.Text == "" {
			t.Fatal("expected response text")
		}
	}
	if initCalls != 1 {
		t.Fatalf("initCalls = %d", initCalls)
	}
}
```

Add a missing-driver test:

```go
func TestNewSessionFailsForUnknownDriverType(t *testing.T) {
	_, err := NewSession(context.Background(), Spec{Type: "missing-driver"})
	if err == nil {
		t.Fatal("expected error for missing driver")
	}
}
```

- [ ] **Step 2: Run the focused session tests to verify they fail**

Run: `go test ./internal/agent -run 'TestNewSessionInitializesDriverRuntimeOnce|TestNewSessionFailsForUnknownDriverType' -v`
Expected: FAIL because `NewSession(context.Context, Spec)` and init-time runtime storage do not exist yet.

- [ ] **Step 3: Refactor `internal/agent/session.go` to store an initialized runtime**

Update `Session` and constructor to:

```go
type Session struct {
	mu      sync.Mutex
	state   SessionState
	spec    Spec
	runtime SessionRuntime
}

func NewSession(ctx context.Context, spec Spec) (*Session, error) {
	driver, ok := LookupDriver(spec.Type)
	if !ok {
		return nil, fmt.Errorf("unknown driver: %s", spec.Type)
	}
	runtime, err := driver.Init(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &Session{
		state:   SessionStateReady,
		runtime: runtime,
		spec:    cloneSpec(spec),
	}, nil
}
```

Update `Send` to run the stored runtime instead of the driver:

```go
func (s *Session) Send(ctx context.Context, req Request) (Response, error) {
	s.mu.Lock()
	s.state = SessionStateBusy
	runtime := s.runtime
	s.mu.Unlock()

	resp, err := runtime.Run(ctx, req)

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

Keep `Matches` and `cloneSpec` intact.

- [ ] **Step 4: Run the focused session tests**

Run: `go test ./internal/agent -run 'TestNewSessionInitializesDriverRuntimeOnce|TestNewSessionFailsForUnknownDriverType' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/session.go internal/agent/session_test.go
git commit -m "feat: initialize driver runtime per session"
```

## Task 3: Refactor manager to resolve sessions by driver type

**Files:**
- Modify: `internal/agent/manager.go`
- Test: `internal/agent/session_test.go`

- [ ] **Step 1: Write the failing manager tests for session recreation**

Add these tests to `internal/agent/session_test.go`:

```go
func TestManagerRecreatesBrokenSessionWithFreshInit(t *testing.T) {
	const name = "test-broken-reinit-driver"
	initCalls := 0
	MustRegisterDriver(name, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			initCalls++
			callNumber := initCalls
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				if callNumber == 1 {
					return Response{}, errors.New("boom")
				}
				return Response{Text: "reply:" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	_, _ = mgr.Send(context.Background(), "bot-1", Spec{Type: name, Command: "codex"}, Request{Prompt: "first"})
	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: name, Command: "codex"}, Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "reply:second" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if initCalls != 2 {
		t.Fatalf("initCalls = %d", initCalls)
	}
}

func TestManagerRecreatesSessionWhenSpecChangesUsingNewDriverType(t *testing.T) {
	const firstDriver = "test-first-driver"
	const secondDriver = "test-second-driver"
	MustRegisterDriver(firstDriver, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: "first:" + req.Prompt}, nil
			}}, nil
		}}
	})
	MustRegisterDriver(secondDriver, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: "second:" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	firstResp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: firstDriver, Command: "codex"}, Request{Prompt: "one"})
	if err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	secondResp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: secondDriver, Command: "claude"}, Request{Prompt: "two"})
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if firstResp.Text != "first:one" || secondResp.Text != "second:two" {
		t.Fatalf("responses = %q / %q", firstResp.Text, secondResp.Text)
	}
}
```

- [ ] **Step 2: Run the focused manager tests to verify they fail**

Run: `go test ./internal/agent -run 'TestManagerRecreatesBrokenSessionWithFreshInit|TestManagerRecreatesSessionWhenSpecChangesUsingNewDriverType' -v`
Expected: FAIL because `Manager` still expects a concrete driver and `NewManager()` without args does not exist.

- [ ] **Step 3: Refactor `internal/agent/manager.go` to stop owning a concrete driver**

Update manager to:

```go
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}
```

Update `sessionFor` to create sessions through `NewSession(ctx, spec)`:

```go
func (m *Manager) Send(ctx context.Context, botID string, spec Spec, req Request) (Response, error) {
	session, err := m.sessionFor(ctx, botID, spec)
	if err != nil {
		return Response{}, err
	}
	return session.Send(ctx, req)
}

func (m *Manager) sessionFor(ctx context.Context, botID string, spec Spec) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[botID]
	if !ok {
		session, err := NewSession(ctx, spec)
		if err != nil {
			return nil, err
		}
		m.sessions[botID] = session
		return session, nil
	}

	if session.Matches(spec) && session.State() != SessionStateBroken {
		return session, nil
	}

	replacement, err := NewSession(ctx, spec)
	if err != nil {
		return nil, err
	}
	m.sessions[botID] = replacement
	return replacement, nil
}
```

- [ ] **Step 4: Run the focused manager tests**

Run: `go test ./internal/agent -run 'TestManagerRecreatesBrokenSessionWithFreshInit|TestManagerRecreatesSessionWhenSpecChangesUsingNewDriverType' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/manager.go internal/agent/session_test.go
git commit -m "feat: resolve agent sessions from driver registry"
```

## Task 4: Adapt the oneshot driver to Init + runtime.Run

**Files:**
- Modify: `internal/agent/driver_oneshot.go`
- Test: `internal/agent/driver_oneshot_test.go`

- [ ] **Step 1: Write the failing oneshot init/runtime test**

Add to `internal/agent/driver_oneshot_test.go`:

```go
func TestOneshotDriverInitReturnsRuntime(t *testing.T) {
	driver := NewOneshotDriver()
	runtime, err := driver.Init(context.Background(), Spec{
		Type:    "oneshot",
		Command: "sh",
		Args:    []string{"-c", "printf 'hello'"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
}
```

- [ ] **Step 2: Run the focused oneshot test to verify it fails**

Run: `go test ./internal/agent -run TestOneshotDriverInitReturnsRuntime -v`
Expected: FAIL because `Init` and `SessionRuntime` are not implemented yet.

- [ ] **Step 3: Refactor `internal/agent/driver_oneshot.go`**

Refactor to:

```go
type OneshotDriver struct{}

type OneshotRuntime struct {
	spec Spec
}

func NewOneshotDriver() *OneshotDriver {
	return &OneshotDriver{}
}

func init() {
	MustRegisterDriver("oneshot", func() Driver {
		return NewOneshotDriver()
	})
}

func (d *OneshotDriver) Init(ctx context.Context, spec Spec) (SessionRuntime, error) {
	_ = ctx
	return &OneshotRuntime{spec: cloneSpec(spec)}, nil
}

func (r *OneshotRuntime) Run(ctx context.Context, req Request) (Response, error) {
	spec := r.spec
	// move the existing command execution logic here unchanged
}
```

Keep existing helpers (`flattenEnv`, `normalizeOutput`) intact and keep all current timeout/output behavior inside `OneshotRuntime.Run`.

- [ ] **Step 4: Run the oneshot tests**

Run: `go test ./internal/agent -run 'TestOneshotDriver' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/driver_oneshot.go internal/agent/driver_oneshot_test.go
git commit -m "feat: initialize oneshot runtime per bot session"
```

## Task 5: Update bootstrap to rely on driver type, not a concrete driver instance

**Files:**
- Modify: `internal/bootstrap/bootstrap.go`
- Test: `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Write the failing bootstrap test**

Add to `internal/bootstrap/bootstrap_test.go`:

```go
func TestBootstrapBuildsConfiguredAgentSpec(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	os.Setenv("CHANNEL_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	defer os.Unsetenv("CHANNEL_MASTER_KEY")
	os.Setenv("AGENT_CLI_COMMAND", "codex")
	defer os.Unsetenv("AGENT_CLI_COMMAND")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SQLitePath = ":memory:"

	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if app.Handler == nil {
		t.Fatal("expected handler")
	}
}
```

This guards that bootstrap still constructs successfully after removing concrete driver injection.

- [ ] **Step 2: Run the focused bootstrap test to verify current behavior**

Run: `go test ./internal/bootstrap -run TestBootstrapBuildsConfiguredAgentSpec -v`
Expected: PASS or FAIL depending on current constructor shape; if it passes, keep it as the guardrail before code change.

- [ ] **Step 3: Refactor bootstrap to stop passing a concrete driver into `agent.NewManager`**

Update `internal/bootstrap/bootstrap.go`:

```go
agentSpec := agent.Spec{
	Type:      "oneshot",
	Command:   cfg.AgentCLICommand,
	Args:      cfg.AgentCLIArgs,
	WorkDir:   cfg.AgentCLIWorkDir,
	Timeout:   cfg.AgentCLITimeout,
	QueueSize: cfg.AgentQueueSize,
}
agentManager := agent.NewManager()
```

Keep bootstrap as the only place selecting `Type`.

- [ ] **Step 4: Run the bootstrap tests**

Run: `go test ./internal/bootstrap -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/bootstrap.go internal/bootstrap/bootstrap_test.go
git commit -m "refactor: let bootstrap select agent driver type"
```

## Task 6: Run final verification

**Files:**
- Modify: none expected
- Test: `./internal/agent/...`, `./internal/bootstrap/...`, `./...`

- [ ] **Step 1: Run focused agent tests**

Run: `go test ./internal/agent/... -v`
Expected: PASS

- [ ] **Step 2: Run focused bootstrap tests**

Run: `go test ./internal/bootstrap/... -v`
Expected: PASS

- [ ] **Step 3: Run the full suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4: Commit final verification state**

```bash
git add internal/agent/*.go internal/bootstrap/*.go
git commit -m "test: verify driver init registry flow"
```

## Self-Review

### Spec coverage
- driver registry added: Task 1
- `Init` once per bot/session: Tasks 2 and 3
- manager resolves by `Spec.Type`: Task 3
- oneshot refactored into init + runtime: Task 4
- bootstrap remains driver selection point: Task 5
- verification of full flow: Task 6

### Placeholder scan
- No `TODO`, `TBD`, or missing-step placeholders remain.
- Every code-changing step includes concrete code blocks.
- Every verification step includes explicit commands and expected results.

### Type consistency
- `Driver`, `SessionRuntime`, `DriverFactory`, `LookupDriver`, and `MustRegisterDriver` are introduced in Task 1 and used consistently afterward.
- `NewSession(ctx, spec)` is introduced in Task 2 and used consistently by the manager in Task 3.
- `OneshotRuntime.Run(ctx, req)` in Task 4 matches the `SessionRuntime` contract defined in Task 1.
- Bootstrap in Task 5 relies on `Spec.Type = "oneshot"`, matching the registration name from Task 4.
