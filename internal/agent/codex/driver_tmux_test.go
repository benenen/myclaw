package codex

import (
	"context"
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

type fakePane struct {
	captures  []string
	sendCalls [][]string
}

func (p *fakePane) SendKeys(keys ...string) error {
	call := make([]string, len(keys))
	copy(call, keys)
	p.sendCalls = append(p.sendCalls, call)
	return nil
}

func (p *fakePane) CapturePane() (string, error) {
	if len(p.captures) == 0 {
		return "", nil
	}
	capture := p.captures[0]
	if len(p.captures) > 1 {
		p.captures = p.captures[1:]
	}
	return capture, nil
}
