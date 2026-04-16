package codex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/gabefiori/gotmux"
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

type tmuxGotmuxFactory struct{}

type tmuxGotmuxSession struct {
	session *gotmux.Session
}

type tmuxGotmuxPane struct {
	target string
}

func init() {
	agent.MustRegisterDriver("codex-tmux", func() agent.Driver {
		return NewTMUXDriver()
	})
}

func NewTMUXDriver() *TMUXDriver {
	return &TMUXDriver{factory: tmuxGotmuxFactory{}}
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
			runtimeFactory = tmuxGotmuxFactory{}
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

func (r *TMUXRuntime) Close() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	session := r.session
	r.session = nil
	r.pane = nil
	r.state = stateBroken
	if r.readErr == nil {
		r.readErr = fmt.Errorf("codex tmux runtime is closed")
	}
	r.mu.Unlock()

	if session == nil {
		return nil
	}
	return session.Kill()
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

func (tmuxGotmuxFactory) Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if len(spec.Args) > 0 {
		return nil, nil, fmt.Errorf("codex tmux driver does not support tmux startup args yet")
	}
	if len(spec.Env) > 0 {
		return nil, nil, fmt.Errorf("codex tmux driver does not support tmux startup env yet")
	}

	config := &gotmux.SessionConfig{Name: sessionName, WindowName: "codex"}
	if strings.TrimSpace(spec.WorkDir) != "" {
		config.Dir = spec.WorkDir
	}
	session, err := gotmux.NewSession(config)
	if err != nil {
		return nil, nil, fmt.Errorf("start tmux session %q: %w", sessionName, err)
	}

	pane := tmuxGotmuxPane{target: sessionName + ":0.0"}
	if err := pane.SendKeys(spec.Command, "C-m"); err != nil {
		_ = session.Kill()
		return nil, nil, fmt.Errorf("start tmux session %q: %w", sessionName, err)
	}
	return tmuxGotmuxSession{session: session}, pane, nil
}

func (s tmuxGotmuxSession) Kill() error {
	if s.session == nil {
		return nil
	}
	if err := s.session.Kill(); err != nil {
		return fmt.Errorf("kill tmux session %q: %w", s.session.Name, err)
	}
	return nil
}

func (p tmuxGotmuxPane) SendKeys(keys ...string) error {
	args := append([]string{"send-keys", "-t", p.target}, keys...)
	cmd, err := gotmux.NewTmuxCmd(args...)
	if err != nil {
		return err
	}
	if err := cmd.Exec(); err != nil {
		return fmt.Errorf("send tmux keys to %q: %w", p.target, err)
	}
	return nil
}

func (p tmuxGotmuxPane) CapturePane() (string, error) {
	cmd, err := gotmux.NewTmuxCmd("capture-pane", "-p", "-t", p.target)
	if err != nil {
		return "", err
	}
	output, err := cmd.ExecWithOutput()
	if err != nil {
		return "", fmt.Errorf("capture tmux pane %q: %w", p.target, err)
	}
	return output, nil
}

var tmuxSessionCounter atomic.Uint64
var tmuxRunCounter atomic.Uint64

func nextTMUXSessionName() string {
	return fmt.Sprintf("myclaw-codex-%d", tmuxSessionCounter.Add(1))
}

func nextTMUXRunID() string {
	return fmt.Sprintf("%d", tmuxRunCounter.Add(1))
}
