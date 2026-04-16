package codex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

type TMUXDriver struct {
	factory tmuxRuntimeFactory
}

type TMUXRuntime struct {
	mu    sync.Mutex
	runMu sync.Mutex

	state   runtimeState
	prompt  string
	pane    tmuxPane
	session tmuxSession
	readErr error
	waitGap time.Duration
}

type tmuxPane interface {
	SendKeys(keys ...string) error
	CapturePane() (string, error)
}

type tmuxSession interface {
	Kill() error
}

type tmuxRuntimeFactory interface {
	Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, error)
}

type tmuxExecFactory struct{}

type tmuxExecSession struct {
	name string
}

type tmuxExecPane struct {
	target string
}

func init() {
	agent.MustRegisterDriver("codex-tmux", func() agent.Driver {
		return NewTMUXDriver()
	})
}

func NewTMUXDriver() *TMUXDriver {
	return &TMUXDriver{factory: tmuxExecFactory{}}
}

func (d *TMUXDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("codex tmux driver requires command")
	}

	runtime := &TMUXRuntime{
		state:   stateStarting,
		prompt:  defaultPrompt,
		waitGap: 10 * time.Millisecond,
	}
	if d != nil {
		runtimeFactory := d.factory
		if runtimeFactory == nil {
			runtimeFactory = tmuxExecFactory{}
		}
		session, pane, err := runtimeFactory.Start(ctx, spec, nextTMUXSessionName())
		if err != nil {
			return nil, err
		}
		runtime.session = session
		runtime.pane = pane
	}

	if err := runtime.waitUntilReady(ctx); err != nil {
		runtime.markBroken(err)
		return nil, err
	}
	return runtime, nil
}

func (r *TMUXRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	promptText := strings.TrimSpace(req.Prompt)
	if promptText == "" {
		return agent.Response{}, fmt.Errorf("codex tmux request prompt is required")
	}

	r.mu.Lock()
	if r.state == stateBroken {
		err := r.readErr
		if err == nil {
			err = fmt.Errorf("codex tmux runtime is broken")
		}
		r.mu.Unlock()
		return agent.Response{}, err
	}
	if r.state != stateReady && r.state != stateStarting {
		state := r.state
		r.mu.Unlock()
		return agent.Response{}, fmt.Errorf("codex tmux runtime is not ready: %s", state)
	}
	pane := r.pane
	prompt := r.prompt
	r.state = stateRunning
	r.mu.Unlock()

	if pane == nil {
		r.markBroken(fmt.Errorf("codex tmux runtime is not connected to a pane"))
		return agent.Response{}, r.currentError()
	}

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		runCtx, cancel = context.WithTimeout(ctx, defaultRunTimeout)
	}
	defer cancel()

	runID := nextTMUXRunID()
	beginMarker := "__MYCLAW_CODEX_RUN_BEGIN_" + runID + "__"
	endMarker := "__MYCLAW_CODEX_RUN_END_" + runID + "__"
	if err := pane.SendKeys(beginMarker, "C-m", promptText, "C-m", endMarker, "C-m"); err != nil {
		r.markBroken(fmt.Errorf("codex tmux send failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	text, err := r.waitRunCompletion(runCtx, beginMarker, endMarker, prompt)
	if err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	r.mu.Lock()
	if r.state != stateBroken {
		r.state = stateReady
	}
	r.mu.Unlock()

	return agent.Response{Text: text, ExitCode: 0, RawOutput: text}, nil
}

func (r *TMUXRuntime) waitUntilReady(ctx context.Context) error {
	r.mu.Lock()
	if r.pane == nil {
		r.state = stateReady
		r.mu.Unlock()
		return nil
	}
	prompt := r.prompt
	if prompt == "" {
		prompt = defaultPrompt
		r.prompt = prompt
	}
	pane := r.pane
	gap := r.waitGap
	r.mu.Unlock()

	if gap <= 0 {
		gap = 10 * time.Millisecond
	}

	for {
		captured, err := pane.CapturePane()
		if err != nil {
			return fmt.Errorf("codex tmux capture failed: %w", err)
		}
		if strings.Contains(normalizeTMUXOutput(captured), prompt) {
			r.mu.Lock()
			if r.state != stateBroken {
				r.state = stateReady
			}
			r.mu.Unlock()
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("codex tmux startup timed out: %w", ctx.Err())
		}
		time.Sleep(gap)
	}
}

func (r *TMUXRuntime) waitRunCompletion(ctx context.Context, beginMarker, endMarker, prompt string) (string, error) {
	r.mu.Lock()
	pane := r.pane
	gap := r.waitGap
	r.mu.Unlock()
	if gap <= 0 {
		gap = 10 * time.Millisecond
	}

	for {
		captured, err := pane.CapturePane()
		if err != nil {
			return "", fmt.Errorf("codex tmux capture failed: %w", err)
		}
		if text, err := extractTMUXRunResult(captured, beginMarker, endMarker, prompt); err == nil {
			return text, nil
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("codex tmux run timed out: %w", ctx.Err())
		}
		time.Sleep(gap)
	}
}

func (r *TMUXRuntime) markBroken(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		err = fmt.Errorf("codex tmux runtime is broken")
	}
	r.readErr = err
	r.state = stateBroken
}

func (r *TMUXRuntime) currentError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return r.readErr
	}
	return fmt.Errorf("codex tmux runtime is broken")
}

func extractTMUXRunResult(text, beginMarker, endMarker, prompt string) (string, error) {
	normalized := normalizeTMUXOutput(text)
	begin := strings.LastIndex(normalized, beginMarker)
	if begin < 0 {
		return "", fmt.Errorf("codex tmux output missing begin marker")
	}
	bodyStart := begin + len(beginMarker)
	endOffset := strings.Index(normalized[bodyStart:], endMarker)
	if endOffset < 0 {
		return "", fmt.Errorf("codex tmux output missing end marker")
	}
	end := bodyStart + endOffset
	after := normalized[end+len(endMarker):]
	if !strings.Contains(after, prompt) {
		return "", fmt.Errorf("codex tmux prompt not restored after end marker")
	}
	return cleanupTMUXRunText(normalized[bodyStart:end]), nil
}

func cleanupTMUXRunText(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "__MYCLAW_CODEX_RUN_BEGIN_") || strings.HasPrefix(trimmed, "__MYCLAW_CODEX_RUN_END_") {
			continue
		}
		cleaned = append(cleaned, strings.TrimRight(line, "\r"))
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func normalizeTMUXOutput(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func (tmuxExecFactory) Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, error) {
	args := append([]string{"new-session", "-d", "-s", sessionName}, spec.Args...)
	args = append(args, spec.Command)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if strings.TrimSpace(spec.WorkDir) != "" {
		cmd.Dir = spec.WorkDir
	}
	if env := flattenEnv(spec.Env); len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, nil, fmt.Errorf("start tmux session %q: %s", sessionName, message)
	}
	return tmuxExecSession{name: sessionName}, tmuxExecPane{target: sessionName + ":0.0"}, nil
}

func (s tmuxExecSession) Kill() error {
	cmd := exec.Command("tmux", "kill-session", "-t", s.name)
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("kill tmux session %q: %s", s.name, message)
	}
	return nil
}

func (p tmuxExecPane) SendKeys(keys ...string) error {
	args := append([]string{"send-keys", "-t", p.target}, keys...)
	cmd := exec.Command("tmux", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("send tmux keys to %q: %s", p.target, message)
	}
	return nil
}

func (p tmuxExecPane) CapturePane() (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", p.target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("capture tmux pane %q: %s", p.target, message)
	}
	return string(output), nil
}

var tmuxRunCounter atomic.Uint64
var tmuxSessionCounter atomic.Uint64

func nextTMUXRunID() string {
	return fmt.Sprintf("%d", tmuxRunCounter.Add(1))
}

func nextTMUXSessionName() string {
	return fmt.Sprintf("myclaw-codex-%d", tmuxSessionCounter.Add(1))
}
