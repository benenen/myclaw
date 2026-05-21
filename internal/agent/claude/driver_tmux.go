package claude

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/tmux"
)

const runtimeTypeClaude = "claude"

type runtimeState string

const (
	stateStarting runtimeState = "starting"
	stateReady    runtimeState = "ready"
	stateRunning  runtimeState = "running"
	stateBroken   runtimeState = "broken"
)

type TMUXDriver struct {
	factory tmuxRuntimeFactory
}

type TMUXRuntime struct {
	mu    sync.Mutex
	runMu sync.Mutex

	state   runtimeState
	pane    tmuxPane
	session tmuxSession
	readErr error
	waitGap time.Duration
	spec    agent.Spec
}

type tmuxPane = tmux.Pane

type tmuxSession = tmux.Session

type tmuxRuntimeFactory interface {
	Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error)
}

var tmuxInitLogf = log.Printf

// NewTMUXDriver creates a new TMUXDriver with default factories.
func NewTMUXDriver() *TMUXDriver {
	return &TMUXDriver{
		factory: tmux.GotmuxFactory{},
	}
}

func init() {
	agent.MustRegisterDriver("claude-tmux", func() agent.Driver {
		return NewTMUXDriver()
	})
}

// Init initializes a new TMUX runtime for the given spec.
func (d *TMUXDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if spec.Command == "" {
		return nil, errors.New("claude tmux driver requires command")
	}
	if spec.WorkDir == "" {
		return nil, errors.New("claude tmux driver requires workdir")
	}

	runtime := &TMUXRuntime{
		state:   stateStarting,
		waitGap: 10 * time.Millisecond,
		spec:    spec,
	}

	if d != nil {
		sessionName := nextTMUXSessionName(spec.BotName)
		startupSpec := spec
		startupSpec.Command = buildTMUXShellCommand(spec)
		session, pane, created, err := d.factory.Start(ctx, startupSpec, sessionName)
		if err != nil {
			return nil, fmt.Errorf("failed to start tmux session: %w", err)
		}
		runtime.session = session
		runtime.pane = pane

		if !created {
			runtime.mu.Lock()
			runtime.state = stateReady
			runtime.mu.Unlock()
			tmuxInitLogf("claude tmux waitUntilReady skipped: bot=%s session=%s created=%t", spec.BotName, sessionName, created)
			return runtime, nil
		}
	}

	err := runtime.waitUntilReady(ctx)
	if err != nil {
		tmuxInitLogf("claude tmux waitUntilReady failed: bot=%s session=%s err=%v", spec.BotName, nextTMUXSessionName(spec.BotName), err)
		runtime.markBroken(err)
		return runtime, nil
	}
	tmuxInitLogf("claude tmux waitUntilReady ok: bot=%s session=%s", spec.BotName, nextTMUXSessionName(spec.BotName))

	return runtime, nil
}

// markBroken sets the runtime to broken state with the given error.
func (r *TMUXRuntime) markBroken(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readErr = err
	r.state = stateBroken
}

// currentError returns the current error if the runtime is broken.
func (r *TMUXRuntime) currentError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return r.readErr
	}
	return errors.New("runtime is broken")
}

// waitUntilReady polls the pane until output appears.
func (r *TMUXRuntime) waitUntilReady(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		output, err := r.pane.CapturePane()
		if err != nil {
			return fmt.Errorf("failed to capture pane: %w", err)
		}

		normalized := tmux.NormalizeTMUXOutput(output)
		if normalized != "" {
			r.mu.Lock()
			r.state = stateReady
			r.mu.Unlock()
			return nil
		}

		time.Sleep(r.waitGap)
	}
}

// Run executes a prompt in the TMUX runtime and returns the response.
func (r *TMUXRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	promptText := strings.TrimSpace(req.Prompt)
	if promptText == "" {
		return agent.Response{}, fmt.Errorf("claude tmux request prompt is required")
	}

	r.mu.Lock()
	if r.state == stateBroken {
		err := r.readErr
		if err == nil {
			err = fmt.Errorf("claude tmux runtime is broken")
		}
		r.mu.Unlock()
		return agent.Response{}, err
	}
	if r.state != stateReady && r.state != stateStarting {
		state := r.state
		r.mu.Unlock()
		return agent.Response{}, fmt.Errorf("claude tmux runtime is not ready: %s", state)
	}
	pane := r.pane
	r.state = stateRunning
	r.mu.Unlock()

	if pane == nil {
		r.markBroken(fmt.Errorf("claude tmux runtime is not connected to a pane"))
		return agent.Response{}, r.currentError()
	}

	if err := pane.SendKeys(promptText, "C-m"); err != nil {
		r.markBroken(fmt.Errorf("claude tmux send failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	captured, err := pane.CapturePane()
	if err != nil {
		r.markBroken(fmt.Errorf("claude tmux capture failed: %w", err))
		return agent.Response{}, r.currentError()
	}
	text := tmux.CleanupTMUXRunText(tmux.NormalizeTMUXOutput(captured))

	r.mu.Lock()
	if r.state != stateBroken {
		r.state = stateReady
	}
	r.mu.Unlock()

	return agent.Response{Text: text, RuntimeType: runtimeTypeClaude, ExitCode: 0, RawOutput: text}, nil
}

// Close terminates the TMUX runtime and cleans up resources.
func (r *TMUXRuntime) Close() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	sess := r.session
	r.session = nil
	r.pane = nil
	r.state = stateBroken
	r.readErr = errors.New("runtime closed")
	r.mu.Unlock()

	if sess != nil {
		return sess.Kill()
	}
	return nil
}

// nextTMUXSessionName generates a session name like "myclaw-claude-<botname>".
func nextTMUXSessionName(botName string) string {
	prefix := strings.TrimSpace(botName)
	prefix = strings.ToLower(prefix)
	prefix = strings.ReplaceAll(prefix, " ", "-")
	if prefix == "" {
		prefix = "claude"
	}
	return fmt.Sprintf("myclaw-claude-%s", prefix)
}

// buildTMUXShellCommand builds the shell command with workspace trust config.
func buildTMUXShellCommand(spec agent.Spec) string {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return ""
	}
	parts := []string{command}
	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		trustConfig := fmt.Sprintf(`projects.%s.trust_level="trusted"`, strconv.Quote(workDir))
		parts = append(parts, "-c "+tmux.ShellQuote(trustConfig))
	}
	return strings.Join(parts, " ")
}
