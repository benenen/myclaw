package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GianlucaP106/gotmux/gotmux"
	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store"
	"github.com/benenen/myclaw/internal/store/repositories"
)

const currentTMUXRunIDFileName = ".myclaw-run-id"

const runtimeTypeClaude = "claude"

var defaultRunTimeout = 30 * time.Second

type runtimeState string

const (
	stateStarting runtimeState = "starting"
	stateReady    runtimeState = "ready"
	stateRunning  runtimeState = "running"
	stateBroken   runtimeState = "broken"
)

type TMUXDriver struct {
	factory         tmuxRuntimeFactory
	runStoreFactory tmuxRunStoreFactory
}

type TMUXRuntime struct {
	mu    sync.Mutex
	runMu sync.Mutex

	state    runtimeState
	pane     tmuxPane
	session  tmuxSession
	readErr  error
	waitGap  time.Duration
	spec     agent.Spec
	runStore tmuxRunStore
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

type tmuxRunRecord struct {
	RunID  string
	Status string
}

type tmuxRunStore interface {
	CreatePending(ctx context.Context, runID, botName, runtimeType string) error
	UpsertDone(ctx context.Context, runID, botName, runtimeType string) error
	GetByRunID(ctx context.Context, runID string) (tmuxRunRecord, error)
}

type tmuxRunStoreFactory interface {
	Open(spec agent.Spec) (tmuxRunStore, error)
}

type tmuxGotmuxFactory struct{}

type sqliteTMUXRunStoreFactory struct{}

type tmuxGotmuxSession struct {
	session *gotmux.Session
}

type tmuxGotmuxPane struct {
	pane *gotmux.Pane
}

// NewTMUXDriver creates a new TMUXDriver with default factories.
func NewTMUXDriver() *TMUXDriver {
	return &TMUXDriver{
		factory:         tmuxGotmuxFactory{},
		runStoreFactory: sqliteTMUXRunStoreFactory{},
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
		session, pane, err := d.factory.Start(ctx, spec, sessionName)
		if err != nil {
			return nil, fmt.Errorf("failed to start tmux session: %w", err)
		}
		runtime.session = session
		runtime.pane = pane

		runStore, err := d.runStoreFactory.Open(spec)
		if err != nil {
			runtime.markBroken(fmt.Errorf("failed to open run store: %w", err))
			return runtime, nil
		}
		runtime.runStore = runStore
	}

	if err := runtime.waitUntilReady(ctx); err != nil {
		runtime.markBroken(err)
		return runtime, nil
	}

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

		normalized := normalizeTMUXOutput(output)
		if normalized != "" {
			r.mu.Lock()
			r.state = stateReady
			r.mu.Unlock()
			return nil
		}

		time.Sleep(r.waitGap)
	}
}

// waitRunCompletion polls the run store until the run is done.
func (r *TMUXRuntime) waitRunCompletion(ctx context.Context, runID string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		record, err := r.runStore.GetByRunID(ctx, runID)
		if err != nil {
			return fmt.Errorf("failed to get run record: %w", err)
		}

		if record.Status == "done" {
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
	if r.runStore == nil {
		r.markBroken(fmt.Errorf("claude tmux runtime is not connected to run state store"))
		return agent.Response{}, r.currentError()
	}

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		runCtx, cancel = context.WithTimeout(ctx, defaultRunTimeout)
	}
	defer cancel()

	runID := domain.NewPrefixedID("run")
	if err := writeTMUXCurrentRunID(r.spec.WorkDir, runID); err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}
	if err := r.runStore.CreatePending(runCtx, runID, r.spec.BotName, runtimeTypeClaude); err != nil {
		r.markBroken(fmt.Errorf("claude tmux create run failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	if err := pane.SendKeys(promptText, "C-m"); err != nil {
		r.markBroken(fmt.Errorf("claude tmux send failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	if err := r.waitRunCompletion(runCtx, runID); err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	captured, err := pane.CapturePane()
	if err != nil {
		r.markBroken(fmt.Errorf("claude tmux capture failed: %w", err))
		return agent.Response{}, r.currentError()
	}
	text := cleanupTMUXRunText(normalizeTMUXOutput(captured))

	r.mu.Lock()
	if r.state != stateBroken {
		r.state = stateReady
	}
	r.mu.Unlock()

	return agent.Response{Text: text, RuntimeType: runtimeTypeClaude, ExitCode: 0, RawOutput: text}, nil
}
