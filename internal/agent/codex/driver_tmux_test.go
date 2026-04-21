package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GianlucaP106/gotmux/gotmux"
	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/tmux"
)

func TestTMUXDriverUsesForkedGotmuxModule(t *testing.T) {
	_ = gotmux.SessionOptions{}
}

func TestTMUXDriverRegistersCodexTMUX(t *testing.T) {
	driver, ok := agent.LookupDriver("codex-tmux")
	if !ok {
		t.Fatal("expected codex-tmux driver registration")
	}
	if driver == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestTMUXDriverInitRejectsEmptyCommand(t *testing.T) {
	driver := NewTMUXDriver()
	_, err := driver.Init(context.Background(), agent.Spec{Type: "codex-tmux"})
	if err == nil {
		t.Fatal("expected empty command error")
	}
}

func TestTMUXDriverInitUsesBotNameInSessionName(t *testing.T) {
	factory := &captureSessionNameFactory{session: &fakeSession{}, pane: &fakePane{captures: []string{"codex>\n"}}}
	driver := &TMUXDriver{factory: factory, runStoreFactory: &fakeRunStoreFactory{store: &fakeRunStore{}}}

	_, err := driver.Init(context.Background(), agent.Spec{
		Type:       "codex-tmux",
		Command:    "/usr/local/bin/codex",
		BotName:    "helper-bot",
		WorkDir:    t.TempDir(),
		SQLitePath: "/tmp/myclaw/myclaw.db",
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if factory.sessionName != "myclaw-codex-helper-bot" {
		t.Fatalf("session name = %q", factory.sessionName)
	}
}

func TestBuildTMUXSessionOptionsUsesWorkspaceAndNotifyCommand(t *testing.T) {
	options := buildTMUXSessionOptions(agent.Spec{
		Command: "/usr/local/bin/codex",
		BotName: "helper-bot",
		WorkDir: "/tmp/myclaw/bots/bot_1/workspace",
	}, "myclaw-codex-helper-bot")

	if options.Name != "myclaw-codex-helper-bot" {
		t.Fatalf("Name = %q", options.Name)
	}
	if options.StartDirectory != "/tmp/myclaw/bots/bot_1/workspace" {
		t.Fatalf("StartDirectory = %q", options.StartDirectory)
	}
	want := `/usr/local/bin/codex -c 'notify=["myclaw", "notify", "codex", "helper-bot"]'`
	if options.ShellCommand != want {
		t.Fatalf("ShellCommand = %q, want %q", options.ShellCommand, want)
	}
}

func TestTMUXRuntimeRunSuccessfulSingleRequest(t *testing.T) {
	workDir := t.TempDir()
	store := &fakeRunStore{
		statuses: []string{"pending", "done"},
	}
	runtime := &TMUXRuntime{
		state:    stateReady,
		pane:     &fakePane{captures: []string{"assistant response: say hello\ncodex>\n"}},
		runStore: store,
		spec: agent.Spec{
			Type:    "codex-tmux",
			BotName: "helper-bot",
			WorkDir: workDir,
		},
	}

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "say hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "assistant response: say hello\ncodex>" {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	pane := runtime.pane.(*fakePane)
	if len(pane.sendCalls) != 1 {
		t.Fatalf("SendKeys() calls = %d, want 1", len(pane.sendCalls))
	}
	want := []string{"say hello", "C-m"}
	for i, got := range pane.sendCalls[0] {
		if got != want[i] {
			t.Fatalf("SendKeys() arg[%d] = %q, want %q", i, got, want[i])
		}
	}
	if len(store.created) != 1 {
		t.Fatalf("CreatePending() calls = %d", len(store.created))
	}
	runIDBytes, err := os.ReadFile(filepath.Join(workDir, currentTMUXRunIDFileName))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(runIDBytes)) != store.created[0] {
		t.Fatalf("run id file = %q, want %q", strings.TrimSpace(string(runIDBytes)), store.created[0])
	}
}

func TestTMUXRuntimeRunMarksBrokenOnSendFailure(t *testing.T) {
	runtime := &TMUXRuntime{
		state:    stateReady,
		pane:     &fakePane{sendErr: errors.New("send boom")},
		runStore: &fakeRunStore{},
		spec: agent.Spec{
			BotName: "helper-bot",
			WorkDir: t.TempDir(),
		},
	}

	_, err := runtime.Run(context.Background(), agent.Request{Prompt: "say hello"})
	if err == nil || !strings.Contains(err.Error(), "codex tmux send failed: send boom") {
		t.Fatalf("Run() error = %v", err)
	}
	if runtime.state != stateBroken {
		t.Fatalf("state = %q", runtime.state)
	}
	if runtime.currentError() == nil || !strings.Contains(runtime.currentError().Error(), "send failed") {
		t.Fatalf("currentError() = %v", runtime.currentError())
	}
}

func TestTMUXRuntimeRunMarksBrokenOnCaptureFailure(t *testing.T) {
	runtime := &TMUXRuntime{
		state:   stateReady,
		waitGap: time.Nanosecond,
		pane: &fakePane{
			captureErrAt: 0,
			captureErr:   errors.New("capture boom"),
		},
		runStore: &fakeRunStore{statuses: []string{"done"}},
		spec: agent.Spec{
			BotName: "helper-bot",
			WorkDir: t.TempDir(),
		},
	}

	_, err := runtime.Run(context.Background(), agent.Request{Prompt: "say hello"})
	if err == nil || !strings.Contains(err.Error(), "codex tmux capture failed: capture boom") {
		t.Fatalf("Run() error = %v", err)
	}
	if runtime.state != stateBroken {
		t.Fatalf("state = %q", runtime.state)
	}
}

func TestTMUXRuntimeRunMarksBrokenOnTimeout(t *testing.T) {
	workDir := t.TempDir()
	runtime := &TMUXRuntime{
		state:    stateReady,
		waitGap:  time.Nanosecond,
		pane:     &fakePane{captures: []string{"still running\n", "still running\n"}},
		runStore: &fakeRunStore{statuses: []string{"pending", "pending", "pending"}},
		spec: agent.Spec{
			Type:    "codex-tmux",
			BotName: "helper-bot",
			WorkDir: workDir,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := runtime.Run(ctx, agent.Request{Prompt: "say hello"})
	if err == nil || !strings.Contains(err.Error(), "codex tmux run timed out") {
		t.Fatalf("Run() error = %v", err)
	}
	if runtime.state != stateBroken {
		t.Fatalf("state = %q", runtime.state)
	}
}

func TestTMUXRuntimeCloseKillsSession(t *testing.T) {
	session := &fakeSession{}
	runtime := &TMUXRuntime{
		state:   stateReady,
		pane:    &fakePane{},
		session: session,
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if session.killCalls != 1 {
		t.Fatalf("Kill() calls = %d", session.killCalls)
	}
	if runtime.session != nil {
		t.Fatal("expected session cleared")
	}
	if runtime.pane != nil {
		t.Fatal("expected pane cleared")
	}
}

func TestTMUXRuntimeCloseReturnsKillFailureAndClearsState(t *testing.T) {
	wantErr := errors.New("kill boom")
	session := &fakeSession{killErr: wantErr}
	runtime := &TMUXRuntime{
		state:   stateReady,
		pane:    &fakePane{},
		session: session,
	}

	err := runtime.Close()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Close() error = %v", err)
	}
	if session.killCalls != 1 {
		t.Fatalf("Kill() calls = %d", session.killCalls)
	}
	if runtime.session != nil {
		t.Fatal("expected session cleared")
	}
	if runtime.pane != nil {
		t.Fatal("expected pane cleared")
	}
	if runtime.state != stateBroken {
		t.Fatalf("state = %q", runtime.state)
	}
	if got := runtime.currentError(); got == nil || got.Error() != "codex tmux runtime is closed" {
		t.Fatalf("currentError() = %v", got)
	}
}

func TestTMUXRuntimeCloseDoesNotHoldLockDuringKill(t *testing.T) {
	killStarted := make(chan struct{})
	releaseKill := make(chan struct{})
	session := &fakeSession{
		kill: func() error {
			close(killStarted)
			<-releaseKill
			return nil
		},
	}
	runtime := &TMUXRuntime{
		state:   stateReady,
		pane:    &fakePane{},
		session: session,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.Close()
	}()

	<-killStarted
	locked := make(chan struct{})
	go func() {
		runtime.mu.Lock()
		runtime.mu.Unlock()
		close(locked)
	}()

	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("runtime mutex remained locked during session kill")
	}

	close(releaseKill)
	if err := <-errCh; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestExecRuntimeUsesCodexRuntimeTypeConstant(t *testing.T) {
	runtime := &ExecRuntime{spec: agent.Spec{
		Type:    execDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexExecDriver", "--", "exec-success"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: time.Second,
	}}

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "你好"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.RuntimeType != runtimeTypeCodex {
		t.Fatalf("Run() runtime type = %q", resp.RuntimeType)
	}
}

type fakePane struct {
	captures     []string
	sendCalls    [][]string
	sendErr      error
	captureErrAt int
	captureErr   error
	captureCalls int
}

type captureSessionNameFactory struct {
	session     tmux.Session
	pane        tmux.Pane
	err         error
	sessionName string
}

type fakeSession struct {
	killCalls int
	killErr   error
	kill      func() error
}

type fakeRunStoreFactory struct {
	store tmuxRunStore
	err   error
}

type fakeRunStore struct {
	created     []string
	statuses    []string
	statusIndex int
	getErr      error
	createErr   error
}

func (f *captureSessionNameFactory) Start(_ context.Context, _ agent.Spec, sessionName string) (tmux.Session, tmux.Pane, error) {
	f.sessionName = sessionName
	return f.session, f.pane, f.err
}

func (f *fakeRunStoreFactory) Open(_ agent.Spec) (tmuxRunStore, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.store, nil
}

func (p *fakePane) SendKeys(keys ...string) error {
	call := make([]string, len(keys))
	copy(call, keys)
	p.sendCalls = append(p.sendCalls, call)
	if p.sendErr != nil {
		return p.sendErr
	}
	return nil
}

func (p *fakePane) CapturePane() (string, error) {
	if p.captureErr != nil && p.captureCalls == p.captureErrAt {
		p.captureCalls++
		return "", p.captureErr
	}
	p.captureCalls++
	if len(p.captures) == 0 {
		return "", nil
	}
	capture := p.captures[0]
	if len(p.captures) > 1 {
		p.captures = p.captures[1:]
	}
	return capture, nil
}

func (s *fakeSession) Kill() error {
	s.killCalls++
	if s.kill != nil {
		return s.kill()
	}
	return s.killErr
}

func (s *fakeRunStore) CreatePending(_ context.Context, runID, _ string, _ string) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, runID)
	return nil
}

func (s *fakeRunStore) UpsertDone(context.Context, string, string, string) error {
	return nil
}

func (s *fakeRunStore) GetByRunID(_ context.Context, runID string) (tmuxRunRecord, error) {
	if s.getErr != nil {
		return tmuxRunRecord{}, s.getErr
	}
	status := "pending"
	if len(s.statuses) > 0 {
		if s.statusIndex >= len(s.statuses) {
			status = s.statuses[len(s.statuses)-1]
		} else {
			status = s.statuses[s.statusIndex]
			s.statusIndex++
		}
	}
	return tmuxRunRecord{RunID: runID, Status: status}, nil
}
