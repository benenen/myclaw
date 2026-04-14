package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type initStubDriver struct {
	init func(context.Context, Spec) (SessionRuntime, error)
}

func (d initStubDriver) Init(ctx context.Context, spec Spec) (SessionRuntime, error) {
	return d.init(ctx, spec)
}

type countingDriver struct{ id int }

func (d countingDriver) Init(context.Context, Spec) (SessionRuntime, error) {
	return runtimeStub{run: func(_ context.Context, _ Request) (Response, error) {
		return Response{}, nil
	}}, nil
}

type runtimeStub struct {
	run func(context.Context, Request) (Response, error)
}

func (r runtimeStub) Run(ctx context.Context, req Request) (Response, error) {
	return r.run(ctx, req)
}

func TestLookupDriverReturnsRegisteredFactory(t *testing.T) {
	const name = "test-registry-driver"
	registerTestDriver(t, name, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, nil
			}}, nil
		}}
	})

	driver, ok := LookupDriver(name)
	if !ok {
		t.Fatal("expected registered driver")
	}
	if driver == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestLookupDriverReturnsFreshDriverInstances(t *testing.T) {
	const name = "test-registry-factory-driver"
	factoryCalls := 0
	registerTestDriver(t, name, func() Driver {
		factoryCalls++
		return countingDriver{id: factoryCalls}
	})

	first, ok := LookupDriver(name)
	if !ok {
		t.Fatal("expected first lookup to succeed")
	}
	second, ok := LookupDriver(name)
	if !ok {
		t.Fatal("expected second lookup to succeed")
	}

	firstDriver, ok := first.(countingDriver)
	if !ok {
		t.Fatalf("first driver type = %T", first)
	}
	secondDriver, ok := second.(countingDriver)
	if !ok {
		t.Fatalf("second driver type = %T", second)
	}
	if firstDriver.id == secondDriver.id {
		t.Fatal("expected lookup to call factory for each request")
	}
}

func TestLookupDriverReturnsFalseForUnknownDriver(t *testing.T) {
	driver, ok := LookupDriver("test-missing-driver")
	if ok {
		t.Fatal("expected lookup to report missing driver")
	}
	if driver != nil {
		t.Fatal("expected missing driver lookup to return nil")
	}
}

func TestRegisterDriverRejectsDuplicateNames(t *testing.T) {
	const name = "test-duplicate-driver"
	factory := func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, nil
			}}, nil
		}}
	}

	if err := RegisterDriver(name, factory); err != nil {
		t.Fatalf("RegisterDriver() first error = %v", err)
	}
	t.Cleanup(func() { unregisterTestDriver(name) })

	if err := RegisterDriver(name, factory); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func TestMustRegisterDriverPanicsOnDuplicate(t *testing.T) {
	const name = "test-must-register-duplicate-driver"
	registerTestDriver(t, name, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, nil
			}}, nil
		}}
	})

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate driver registration")
		}
	}()

	MustRegisterDriver(name, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, nil
			}}, nil
		}}
	})
}

func TestSessionInitializesDriverOnConstruction(t *testing.T) {
	initCalls := 0
	var gotSpec Spec
	session, err := NewSession(context.Background(), initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
		initCalls++
		gotSpec = spec
		return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
			return Response{Text: spec.Command + ":" + req.Prompt}, nil
		}}, nil
	}}, Spec{Command: "codex"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	if initCalls != 1 {
		t.Fatalf("initCalls after NewSession = %d", initCalls)
	}
	if gotSpec.Command != "codex" {
		t.Fatalf("init spec command = %q", gotSpec.Command)
	}

	resp, err := session.Send(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "codex:hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if initCalls != 1 {
		t.Fatalf("initCalls after Send = %d", initCalls)
	}
}

func TestSessionConstructionFailsWhenDriverInitFails(t *testing.T) {
	wantErr := context.DeadlineExceeded

	session, err := NewSession(context.Background(), initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
		return nil, wantErr
	}}, Spec{Command: "codex"})
	if err == nil {
		t.Fatal("NewSession() error = nil")
	}
	if session != nil {
		t.Fatalf("session = %#v", session)
	}
}

func TestSessionSendClonesSpecProvidedAtInit(t *testing.T) {
	env := map[string]string{"KEY": "value"}
	args := []string{"run"}
	var gotSpec Spec
	session, err := NewSession(context.Background(), initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
		gotSpec = spec
		return runtimeStub{run: func(_ context.Context, _ Request) (Response, error) {
			return Response{Text: spec.Args[0] + ":" + spec.Env["KEY"]}, nil
		}}, nil
	}}, Spec{Command: "codex", Args: args, Env: env})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	args[0] = "mutated"
	env["KEY"] = "changed"

	resp, err := session.Send(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "run:value" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if gotSpec.Args[0] != "run" {
		t.Fatalf("gotSpec.Args[0] = %q", gotSpec.Args[0])
	}
	if gotSpec.Env["KEY"] != "value" {
		t.Fatalf("gotSpec.Env[KEY] = %q", gotSpec.Env["KEY"])
	}
}

func TestSessionSendSerializesConcurrentCalls(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var concurrent int32
	var maxConcurrent int32

	session, err := NewSession(context.Background(), initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
		return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
			current := atomic.AddInt32(&concurrent, 1)
			defer atomic.AddInt32(&concurrent, -1)
			for {
				seen := atomic.LoadInt32(&maxConcurrent)
				if current <= seen || atomic.CompareAndSwapInt32(&maxConcurrent, seen, current) {
					break
				}
			}
			started <- req.Prompt
			<-release
			return Response{Text: req.Prompt}, nil
		}}, nil
	}}, Spec{Command: "codex"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan Response, 2)
	errs := make(chan error, 2)
	for _, prompt := range []string{"first", "second"} {
		wg.Add(1)
		go func(prompt string) {
			defer wg.Done()
			resp, err := session.Send(context.Background(), Request{Prompt: prompt})
			results <- resp
			errs <- err
		}(prompt)
	}

	firstStarted := <-started
	if firstStarted != "first" && firstStarted != "second" {
		t.Fatalf("started prompt = %q", firstStarted)
	}
	if got := atomic.LoadInt32(&concurrent); got != 1 {
		t.Fatalf("concurrent runs while first active = %d", got)
	}

	release <- struct{}{}
	secondStarted := <-started
	if secondStarted != "first" && secondStarted != "second" {
		t.Fatalf("started prompt = %q", secondStarted)
	}
	if secondStarted == firstStarted {
		t.Fatalf("started prompts = %q, %q", firstStarted, secondStarted)
	}
	release <- struct{}{}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("max concurrent runs = %d", got)
	}
	if got := session.State(); got != SessionStateReady {
		t.Fatalf("State() after sends = %q", got)
	}
}

func TestManagerReinitializesBrokenSession(t *testing.T) {
	const driverName = "test-manager-broken-session-driver"
	initCalls := 0
	var runCalls int32
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			initCalls++
			myInit := initCalls
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				atomic.AddInt32(&runCalls, 1)
				if myInit == 1 {
					return Response{}, errors.New("boom")
				}
				return Response{Text: req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	if _, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName, Command: "codex"}, Request{Prompt: "one"}); err == nil {
		t.Fatal("first Send() error = nil")
	}
	if got := mgr.State("bot-1"); got != SessionStateBroken {
		t.Fatalf("State() after broken send = %q", got)
	}

	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName, Command: "codex"}, Request{Prompt: "two"})
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if resp.Text != "two" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if initCalls != 2 {
		t.Fatalf("initCalls = %d", initCalls)
	}
	if got := atomic.LoadInt32(&runCalls); got != 2 {
		t.Fatalf("runCalls = %d", got)
	}
	if got := mgr.State("bot-1"); got != SessionStateReady {
		t.Fatalf("State() after recovery = %q", got)
	}
}

func TestManagerRecreatesSessionWhenSpecChangesDuringConcurrentLookup(t *testing.T) {
	const driverName = "test-manager-concurrent-spec-change-driver"
	initCalls := make(chan string, 4)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
			initCalls <- spec.Command
			ready <- struct{}{}
			<-release
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: spec.Command + ":" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	var wg sync.WaitGroup
	responses := make(chan Response, 2)
	errs := make(chan error, 2)
	for _, spec := range []Spec{{Type: driverName, Command: "alpha"}, {Type: driverName, Command: "beta"}} {
		wg.Add(1)
		go func(spec Spec) {
			defer wg.Done()
			resp, err := mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "hello"})
			responses <- resp
			errs <- err
		}(spec)
	}

	<-ready
	<-ready
	release <- struct{}{}
	release <- struct{}{}
	wg.Wait()
	close(responses)
	close(errs)
	close(initCalls)

	seenResponses := map[string]bool{}
	for resp := range responses {
		seenResponses[resp.Text] = true
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
	seenInits := map[string]int{}
	for command := range initCalls {
		seenInits[command]++
	}
	if seenInits["alpha"] == 0 || seenInits["beta"] == 0 {
		t.Fatalf("init calls = %#v", seenInits)
	}
	if !seenResponses["alpha:hello"] || !seenResponses["beta:hello"] {
		t.Fatalf("responses = %#v", seenResponses)
	}

	finalResp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName, Command: "beta"}, Request{Prompt: "again"})
	if err != nil {
		t.Fatalf("final Send() error = %v", err)
	}
	if finalResp.Text != "beta:again" {
		t.Fatalf("final resp.Text = %q", finalResp.Text)
	}
}

func registerTestDriver(t *testing.T, name string, factory DriverFactory) {
	t.Helper()
	if err := RegisterDriver(name, factory); err != nil {
		t.Fatalf("RegisterDriver() error = %v", err)
	}
	t.Cleanup(func() { unregisterTestDriver(name) })
}

func TestManagerUsesRegisteredDriverBySpecType(t *testing.T) {
	const driverName = "test-manager-registry-driver"
	initCalls := 0
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
			initCalls++
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: spec.Type + ":" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName}, Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != driverName+":hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if initCalls != 1 {
		t.Fatalf("initCalls = %d", initCalls)
	}
}

func TestManagerReusesSessionRuntimeForMatchingSpec(t *testing.T) {
	const driverName = "test-manager-reuse-driver"
	initCalls := 0
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
			initCalls++
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: spec.Type + ":" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	for _, prompt := range []string{"one", "two"} {
		if _, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName, Command: "codex"}, Request{Prompt: prompt}); err != nil {
			t.Fatalf("Send(%q) error = %v", prompt, err)
		}
	}
	if initCalls != 1 {
		t.Fatalf("initCalls = %d", initCalls)
	}
}

func TestManagerRecreatesSessionWhenSpecChanges(t *testing.T) {
	const driverName = "test-manager-recreate-driver"
	initCalls := 0
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
			initCalls++
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: spec.Command + ":" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	if _, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName, Command: "codex"}, Request{Prompt: "one"}); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: driverName, Command: "claude"}, Request{Prompt: "two"})
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if resp.Text != "claude:two" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if initCalls != 2 {
		t.Fatalf("initCalls = %d", initCalls)
	}
}

func TestManagerReturnsErrorForUnknownDriverType(t *testing.T) {
	mgr := NewManager()

	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Type: "test-missing-driver"}, Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("Send() error = nil")
	}
	if resp != (Response{}) {
		t.Fatalf("resp = %#v", resp)
	}
	if got := mgr.State("bot-1"); got != SessionStateStopped {
		t.Fatalf("State() = %q", got)
	}
}

func unregisterTestDriver(name string) {
	driversMu.Lock()
	defer driversMu.Unlock()
	delete(drivers, name)
}
