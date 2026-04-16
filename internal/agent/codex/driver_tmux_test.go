package codex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

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

func TestTMUXRuntimeRunSuccessfulSingleRequest(t *testing.T) {
	tmuxRunCounter.Store(0)

	runtime := &TMUXRuntime{
		state:  stateReady,
		prompt: "codex>",
		pane: &fakePane{
			captures: []string{
				"codex>\n",
				"__MYCLAW_CODEX_RUN_BEGIN_1__\nassistant response: say hello\n__MYCLAW_CODEX_RUN_END_1__\ncodex>\n",
			},
		},
		waitGap: time.Nanosecond,
	}

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "say hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "assistant response: say hello" {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	pane := runtime.pane.(*fakePane)
	if len(pane.sendCalls) != 1 {
		t.Fatalf("SendKeys() calls = %d, want 1", len(pane.sendCalls))
	}
	want := []string{"__MYCLAW_CODEX_RUN_BEGIN_1__", "C-m", "say hello", "C-m", "__MYCLAW_CODEX_RUN_END_1__", "C-m"}
	for i, got := range pane.sendCalls[0] {
		if got != want[i] {
			t.Fatalf("SendKeys() arg[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestTMUXRuntimeRunMarksBrokenOnSendFailure(t *testing.T) {
	runtime := &TMUXRuntime{
		state:  stateReady,
		prompt: "codex>",
		pane:   &fakePane{sendErr: errors.New("send boom")},
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
		prompt:  "codex>",
		waitGap: time.Nanosecond,
		pane: &fakePane{
			captureErrAt: 0,
			captureErr:   errors.New("capture boom"),
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
	runtime := &TMUXRuntime{
		state:   stateReady,
		prompt:  "codex>",
		waitGap: time.Nanosecond,
		pane:    &fakePane{captures: []string{"still running\n", "still running\n"}},
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
		prompt:  "codex>",
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
		prompt:  "codex>",
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
		prompt:  "codex>",
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

func TestExtractTMUXRunResultRequiresRestoredPrompt(t *testing.T) {
	_, err := extractTMUXRunResult("__MYCLAW_CODEX_RUN_BEGIN_1__\nhello\n__MYCLAW_CODEX_RUN_END_1__\n", "__MYCLAW_CODEX_RUN_BEGIN_1__", "__MYCLAW_CODEX_RUN_END_1__", "codex>")
	if err == nil || !strings.Contains(err.Error(), "prompt not restored") {
		t.Fatalf("extractTMUXRunResult() error = %v", err)
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

type fakeSession struct {
	killCalls int
	killErr   error
	kill      func() error
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
