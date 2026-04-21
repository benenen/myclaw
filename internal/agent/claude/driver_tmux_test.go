package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

// mockTMUXPane is a mock implementation of tmuxPane.
type mockTMUXPane struct {
	sendKeysFunc    func(keys ...string) error
	capturePaneFunc func() (string, error)
}

func (m *mockTMUXPane) SendKeys(keys ...string) error {
	if m.sendKeysFunc != nil {
		return m.sendKeysFunc(keys...)
	}
	return nil
}

func (m *mockTMUXPane) CapturePane() (string, error) {
	if m.capturePaneFunc != nil {
		return m.capturePaneFunc()
	}
	return "", nil
}

// mockTMUXSession is a mock implementation of tmuxSession.
type mockTMUXSession struct {
	killFunc func() error
}

func (m *mockTMUXSession) Kill() error {
	if m.killFunc != nil {
		return m.killFunc()
	}
	return nil
}

// mockTMUXRuntimeFactory is a mock implementation of tmuxRuntimeFactory.
type mockTMUXRuntimeFactory struct {
	startFunc func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, error)
}

func (m *mockTMUXRuntimeFactory) Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, error) {
	if m.startFunc != nil {
		return m.startFunc(ctx, spec, sessionName)
	}
	return &mockTMUXSession{}, &mockTMUXPane{}, nil
}

// mockTMUXRunStore is a mock implementation of tmuxRunStore.
type mockTMUXRunStore struct {
	createPendingFunc func(ctx context.Context, runID, botName, runtimeType string) error
	upsertDoneFunc    func(ctx context.Context, runID, botName, runtimeType string) error
	getByRunIDFunc    func(ctx context.Context, runID string) (tmuxRunRecord, error)
}

func (m *mockTMUXRunStore) CreatePending(ctx context.Context, runID, botName, runtimeType string) error {
	if m.createPendingFunc != nil {
		return m.createPendingFunc(ctx, runID, botName, runtimeType)
	}
	return nil
}

func (m *mockTMUXRunStore) UpsertDone(ctx context.Context, runID, botName, runtimeType string) error {
	if m.upsertDoneFunc != nil {
		return m.upsertDoneFunc(ctx, runID, botName, runtimeType)
	}
	return nil
}

func (m *mockTMUXRunStore) GetByRunID(ctx context.Context, runID string) (tmuxRunRecord, error) {
	if m.getByRunIDFunc != nil {
		return m.getByRunIDFunc(ctx, runID)
	}
	return tmuxRunRecord{}, nil
}

// mockTMUXRunStoreFactory is a mock implementation of tmuxRunStoreFactory.
type mockTMUXRunStoreFactory struct {
	openFunc func(spec agent.Spec) (tmuxRunStore, error)
}

func (m *mockTMUXRunStoreFactory) Open(spec agent.Spec) (tmuxRunStore, error) {
	if m.openFunc != nil {
		return m.openFunc(spec)
	}
	return &mockTMUXRunStore{}, nil
}

// TestTMUXDriver_Init_Success tests successful initialization with valid spec.
func TestTMUXDriver_Init_Success(t *testing.T) {
	ctx := context.Background()
	spec := agent.Spec{
		Command: "claude",
		WorkDir: "/tmp/test",
	}

	mockPane := &mockTMUXPane{
		capturePaneFunc: func() (string, error) {
			return "ready", nil
		},
	}
	mockSession := &mockTMUXSession{}
	mockFactory := &mockTMUXRuntimeFactory{
		startFunc: func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, error) {
			return mockSession, mockPane, nil
		},
	}
	mockStoreFactory := &mockTMUXRunStoreFactory{}

	driver := &TMUXDriver{
		factory:         mockFactory,
		runStoreFactory: mockStoreFactory,
	}

	runtime, err := driver.Init(ctx, spec)
	if err != nil {
		t.Fatalf("Init() failed: %v", err)
	}

	if runtime == nil {
		t.Fatal("Init() returned nil runtime")
	}

	tmuxRuntime, ok := runtime.(*TMUXRuntime)
	if !ok {
		t.Fatal("Init() did not return *TMUXRuntime")
	}

	if tmuxRuntime.spec.Command != spec.Command {
		t.Errorf("expected Command %q, got %q", spec.Command, tmuxRuntime.spec.Command)
	}
	if tmuxRuntime.spec.WorkDir != spec.WorkDir {
		t.Errorf("expected WorkDir %q, got %q", spec.WorkDir, tmuxRuntime.spec.WorkDir)
	}
}

// TestTMUXDriver_Init_MissingCommand tests that Init returns an error when Command is empty.
func TestTMUXDriver_Init_MissingCommand(t *testing.T) {
	ctx := context.Background()
	spec := agent.Spec{
		Command: "",
		WorkDir: "/tmp/test",
	}

	mockFactory := &mockTMUXRuntimeFactory{}
	mockStoreFactory := &mockTMUXRunStoreFactory{}

	driver := &TMUXDriver{
		factory:         mockFactory,
		runStoreFactory: mockStoreFactory,
	}

	_, err := driver.Init(ctx, spec)
	if err == nil {
		t.Fatal("Init() should have failed with empty Command")
	}
}

// TestTMUXDriver_Init_MissingWorkDir tests that Init returns an error when WorkDir is empty.
func TestTMUXDriver_Init_MissingWorkDir(t *testing.T) {
	ctx := context.Background()
	spec := agent.Spec{
		Command: "claude",
		WorkDir: "",
	}

	mockFactory := &mockTMUXRuntimeFactory{}
	mockStoreFactory := &mockTMUXRunStoreFactory{}

	driver := &TMUXDriver{
		factory:         mockFactory,
		runStoreFactory: mockStoreFactory,
	}

	_, err := driver.Init(ctx, spec)
	if err == nil {
		t.Fatal("Init() should have failed with empty WorkDir")
	}
}

// TestTMUXRuntime_Run_Success tests successful run with mocks.
func TestTMUXRuntime_Run_Success(t *testing.T) {
	pane := &mockTMUXPane{
		capturePaneFunc: func() (string, error) {
			return "test output\n", nil
		},
	}

	runStore := &mockTMUXRunStore{
		getByRunIDFunc: func(ctx context.Context, runID string) (tmuxRunRecord, error) {
			return tmuxRunRecord{RunID: runID, Status: "done"}, nil
		},
	}

	runtime := &TMUXRuntime{
		state:    stateReady,
		pane:     pane,
		session:  &mockTMUXSession{},
		waitGap:  1 * time.Millisecond,
		spec: agent.Spec{
			BotName: "test-bot",
			WorkDir: t.TempDir(),
		},
		runStore: runStore,
	}

	req := agent.Request{
		Prompt: "test prompt",
	}

	ctx := context.Background()
	resp, err := runtime.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.RuntimeType != runtimeTypeClaude {
		t.Errorf("expected runtime type %q, got %q", runtimeTypeClaude, resp.RuntimeType)
	}
	if resp.Text == "" {
		t.Error("expected non-empty response text")
	}
}

// TestTMUXRuntime_Run_EmptyPrompt tests error when prompt is empty.
func TestTMUXRuntime_Run_EmptyPrompt(t *testing.T) {
	runtime := &TMUXRuntime{
		state: stateReady,
	}

	req := agent.Request{
		Prompt: "",
	}

	ctx := context.Background()
	_, err := runtime.Run(ctx, req)
	if err == nil {
		t.Fatal("Run should fail with empty prompt")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTMUXRuntime_Run_BrokenState tests error when runtime is broken.
func TestTMUXRuntime_Run_BrokenState(t *testing.T) {
	runtime := &TMUXRuntime{
		state:   stateBroken,
		readErr: errors.New("test error"),
	}

	req := agent.Request{
		Prompt: "test",
	}

	ctx := context.Background()
	_, err := runtime.Run(ctx, req)
	if err == nil {
		t.Fatal("Run should fail with broken state")
	}
}
