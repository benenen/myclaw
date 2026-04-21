package claude

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/tmux"
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
	startFunc func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error)
}

func (m *mockTMUXRuntimeFactory) Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error) {
	if m.startFunc != nil {
		return m.startFunc(ctx, spec, sessionName)
	}
	return &mockTMUXSession{}, &mockTMUXPane{}, true, nil
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
	originalExecutable := currentExecutablePath
	currentExecutablePath = func() (string, error) { return "/abs/path/myclaw", nil }
	defer func() { currentExecutablePath = originalExecutable }()
	originalLogf := tmuxInitLogf
	tmuxInitLogf = func(string, ...any) {}
	defer func() { tmuxInitLogf = originalLogf }()
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
		startFunc: func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error) {
			return mockSession, mockPane, true, nil
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

func TestTMUXDriverInitLogsWaitUntilReadyOutcome(t *testing.T) {
	ctx := context.Background()
	originalExecutable := currentExecutablePath
	currentExecutablePath = func() (string, error) { return "/abs/path/myclaw", nil }
	defer func() { currentExecutablePath = originalExecutable }()

	var logs []string
	originalLogf := tmuxInitLogf
	tmuxInitLogf = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	defer func() { tmuxInitLogf = originalLogf }()

	driver := &TMUXDriver{
		factory: &mockTMUXRuntimeFactory{
			startFunc: func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error) {
				return &mockTMUXSession{}, &mockTMUXPane{
					capturePaneFunc: func() (string, error) {
						return "ready", nil
					},
				}, true, nil
			},
		},
		runStoreFactory: &mockTMUXRunStoreFactory{},
	}

	_, err := driver.Init(ctx, agent.Spec{Command: "claude", WorkDir: "/tmp/test", BotName: "helper-bot"})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "claude tmux waitUntilReady ok: bot=helper-bot session=myclaw-claude-helper-bot") {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestTMUXDriverInitLogsWaitUntilReadyFailure(t *testing.T) {
	ctx := context.Background()
	originalExecutable := currentExecutablePath
	currentExecutablePath = func() (string, error) { return "/abs/path/myclaw", nil }
	defer func() { currentExecutablePath = originalExecutable }()

	var logs []string
	originalLogf := tmuxInitLogf
	tmuxInitLogf = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	defer func() { tmuxInitLogf = originalLogf }()

	driver := &TMUXDriver{
		factory: &mockTMUXRuntimeFactory{
			startFunc: func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error) {
				return &mockTMUXSession{}, &mockTMUXPane{
					capturePaneFunc: func() (string, error) {
						return "", errors.New("capture boom")
					},
				}, true, nil
			},
		},
		runStoreFactory: &mockTMUXRunStoreFactory{},
	}

	runtime, err := driver.Init(ctx, agent.Spec{Command: "claude", WorkDir: "/tmp/test", BotName: "helper-bot"})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if runtime == nil {
		t.Fatal("expected runtime")
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "claude tmux waitUntilReady failed: bot=helper-bot session=myclaw-claude-helper-bot err=failed to capture pane: capture boom") {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestTMUXDriverInitSkipsWaitUntilReadyForExistingSession(t *testing.T) {
	ctx := context.Background()
	originalExecutable := currentExecutablePath
	currentExecutablePath = func() (string, error) { return "/abs/path/myclaw", nil }
	defer func() { currentExecutablePath = originalExecutable }()

	captureCalls := 0
	var logs []string
	originalLogf := tmuxInitLogf
	tmuxInitLogf = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	defer func() { tmuxInitLogf = originalLogf }()

	driver := &TMUXDriver{
		factory: &mockTMUXRuntimeFactory{
			startFunc: func(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error) {
				return &mockTMUXSession{}, &mockTMUXPane{
					capturePaneFunc: func() (string, error) {
						captureCalls++
						return "", errors.New("should not capture")
					},
				}, false, nil
			},
		},
		runStoreFactory: &mockTMUXRunStoreFactory{},
	}

	runtime, err := driver.Init(ctx, agent.Spec{Command: "claude", WorkDir: "/tmp/test", BotName: "helper-bot"})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if runtime == nil {
		t.Fatal("expected runtime")
	}
	if captureCalls != 0 {
		t.Fatalf("captureCalls = %d, want 0", captureCalls)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "claude tmux waitUntilReady skipped: bot=helper-bot session=myclaw-claude-helper-bot created=false") {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestBuildTMUXShellCommandUsesAbsoluteNotifyPath(t *testing.T) {
	originalExecutable := currentExecutablePath
	currentExecutablePath = func() (string, error) { return "/abs/path/myclaw", nil }
	defer func() { currentExecutablePath = originalExecutable }()

	got := buildTMUXShellCommand(agent.Spec{
		Command: "claude",
		BotName: "helper-bot",
		WorkDir: "/tmp/workspace",
	})
	want := `claude -c 'notify=["/abs/path/myclaw", "notify", "claude", "helper-bot"]' -c 'projects."/tmp/workspace".trust_level="trusted"'`
	if got != want {
		t.Fatalf("buildTMUXShellCommand() = %q, want %q", got, want)
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
		state:   stateReady,
		pane:    pane,
		session: &mockTMUXSession{},
		waitGap: 1 * time.Millisecond,
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

// TestTMUXRuntime_Close tests Close method kills session and sets state.
func TestTMUXRuntime_Close(t *testing.T) {
	t.Run("close with session", func(t *testing.T) {
		killed := false
		session := &mockTMUXSession{
			killFunc: func() error {
				killed = true
				return nil
			},
		}
		pane := &mockTMUXPane{}

		runtime := &TMUXRuntime{
			state:   stateReady,
			session: session,
			pane:    pane,
		}

		err := runtime.Close()
		if err != nil {
			t.Fatalf("Close() failed: %v", err)
		}
		if !killed {
			t.Error("Close() did not kill session")
		}
		if runtime.state != stateBroken {
			t.Errorf("expected state %q, got %q", stateBroken, runtime.state)
		}
		if runtime.session != nil {
			t.Error("Close() did not clear session")
		}
		if runtime.pane != nil {
			t.Error("Close() did not clear pane")
		}
		if runtime.readErr == nil || !strings.Contains(runtime.readErr.Error(), "runtime closed") {
			t.Errorf("expected readErr to contain 'runtime closed', got %v", runtime.readErr)
		}
	})

	t.Run("close with nil runtime", func(t *testing.T) {
		var runtime *TMUXRuntime
		err := runtime.Close()
		if err != nil {
			t.Fatalf("Close() on nil runtime should not error: %v", err)
		}
	})

	t.Run("close with nil session", func(t *testing.T) {
		runtime := &TMUXRuntime{
			state:   stateReady,
			session: nil,
			pane:    &mockTMUXPane{},
		}

		err := runtime.Close()
		if err != nil {
			t.Fatalf("Close() with nil session failed: %v", err)
		}
		if runtime.state != stateBroken {
			t.Errorf("expected state %q, got %q", stateBroken, runtime.state)
		}
	})

	t.Run("close with kill error", func(t *testing.T) {
		killErr := errors.New("kill failed")
		session := &mockTMUXSession{
			killFunc: func() error {
				return killErr
			},
		}

		runtime := &TMUXRuntime{
			state:   stateReady,
			session: session,
		}

		err := runtime.Close()
		if err == nil {
			t.Fatal("Close() should return kill error")
		}
		if !strings.Contains(err.Error(), "kill failed") {
			t.Errorf("expected error to contain 'kill failed', got %v", err)
		}
	})
}

// TestNextTMUXSessionName tests session naming with various inputs.
func TestNextTMUXSessionName(t *testing.T) {
	tests := []struct {
		name     string
		botName  string
		expected string
	}{
		{
			name:     "simple name",
			botName:  "mybot",
			expected: "myclaw-claude-mybot",
		},
		{
			name:     "name with spaces",
			botName:  "my bot",
			expected: "myclaw-claude-my-bot",
		},
		{
			name:     "uppercase name",
			botName:  "MyBot",
			expected: "myclaw-claude-mybot",
		},
		{
			name:     "empty name",
			botName:  "",
			expected: "myclaw-claude-claude",
		},
		{
			name:     "whitespace only",
			botName:  "   ",
			expected: "myclaw-claude-claude",
		},
		{
			name:     "multiple spaces",
			botName:  "my  bot  name",
			expected: "myclaw-claude-my--bot--name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nextTMUXSessionName(tt.botName)
			if result != tt.expected {
				t.Errorf("nextTMUXSessionName(%q) = %q, want %q", tt.botName, result, tt.expected)
			}
		})
	}
}

// TestCleanupTMUXRunText tests text cleanup.
func TestCleanupTMUXRunText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple text",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "text with empty lines",
			input:    "line1\n\nline2\n\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "text with carriage returns",
			input:    "line1\r\nline2\r\nline3\r",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "text with leading/trailing whitespace",
			input:    "  \n  line1  \n  line2  \n  ",
			expected: "line1  \n  line2",
		},
		{
			name:     "empty text",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   \n   \n   ",
			expected: "",
		},
		{
			name:     "mixed whitespace and content",
			input:    "\n\nhello\n\n\nworld\n\n",
			expected: "hello\nworld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tmux.CleanupTMUXRunText(tt.input)
			if result != tt.expected {
				t.Errorf("cleanupTMUXRunText(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestShellQuote tests shell quoting.
func TestShellQuote(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple text",
			input:    "hello",
			expected: "'hello'",
		},
		{
			name:     "text with spaces",
			input:    "hello world",
			expected: "'hello world'",
		},
		{
			name:     "text with single quote",
			input:    "it's",
			expected: "'it'\\''s'",
		},
		{
			name:     "text with multiple single quotes",
			input:    "it's a 'test'",
			expected: "'it'\\''s a '\\''test'\\'''",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "''",
		},
		{
			name:     "special characters",
			input:    "hello$world",
			expected: "'hello$world'",
		},
		{
			name:     "newline",
			input:    "hello\nworld",
			expected: "'hello\nworld'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tmux.ShellQuote(tt.input)
			if result != tt.expected {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
