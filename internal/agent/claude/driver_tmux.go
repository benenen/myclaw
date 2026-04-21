package claude

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	"github.com/benenen/myclaw/internal/tmux"
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

type tmuxPane = tmux.Pane

type tmuxSession = tmux.Session

type tmuxRuntimeFactory interface {
	Start(ctx context.Context, spec agent.Spec, sessionName string) (tmuxSession, tmuxPane, bool, error)
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

type sqliteTMUXRunStoreFactory struct{}

var currentExecutablePath = os.Executable
var tmuxInitLogf = log.Printf

// NewTMUXDriver creates a new TMUXDriver with default factories.
func NewTMUXDriver() *TMUXDriver {
	return &TMUXDriver{
		factory:         tmux.GotmuxFactory{},
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
		startupSpec := spec
		startupSpec.Command = buildTMUXShellCommand(spec)
		session, pane, created, err := d.factory.Start(ctx, startupSpec, sessionName)
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

// writeTMUXCurrentRunID writes the run ID to the .myclaw-run-id file.
func writeTMUXCurrentRunID(workDir, runID string) error {
	if strings.TrimSpace(workDir) == "" {
		return fmt.Errorf("claude tmux workdir is required")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("claude tmux prepare workdir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, currentTMUXRunIDFileName), []byte(runID+"\n"), 0o644); err != nil {
		return fmt.Errorf("claude tmux write current run id: %w", err)
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

// buildTMUXShellCommand builds the shell command with notify config.
func buildTMUXShellCommand(spec agent.Spec) string {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return ""
	}
	notifyCommand := "myclaw"
	if executable, err := currentExecutablePath(); err == nil && strings.TrimSpace(executable) != "" {
		notifyCommand = executable
	}
	notifyConfig := fmt.Sprintf(`notify=[%s, "notify", "claude", %s]`, strconv.Quote(notifyCommand), strconv.Quote(spec.BotName))
	parts := []string{
		command,
		"-c " + tmux.ShellQuote(notifyConfig),
	}
	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		trustConfig := fmt.Sprintf(`projects.%s.trust_level="trusted"`, strconv.Quote(workDir))
		parts = append(parts, "-c "+tmux.ShellQuote(trustConfig))
	}
	return strings.Join(parts, " ")
}

// buildTMUXSessionOptions creates SessionOptions with name, shell command, and start directory.
func buildTMUXSessionOptions(spec agent.Spec, sessionName string) *gotmux.SessionOptions {
	options := &gotmux.SessionOptions{
		Name:         sessionName,
		ShellCommand: buildTMUXShellCommand(spec),
	}
	if strings.TrimSpace(spec.WorkDir) != "" {
		options.StartDirectory = spec.WorkDir
	}
	return options
}

// sqliteTMUXRunStore implements tmuxRunStore using SQLite.
type sqliteTMUXRunStore struct {
	repo *repositories.AgentRunRepository
}

// Open opens a SQLite run store for the given spec.
func (sqliteTMUXRunStoreFactory) Open(spec agent.Spec) (tmuxRunStore, error) {
	if strings.TrimSpace(spec.SQLitePath) == "" {
		return nil, fmt.Errorf("claude tmux driver requires sqlite path")
	}
	db, err := store.Open(spec.SQLitePath)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(db); err != nil {
		return nil, err
	}
	return &sqliteTMUXRunStore{repo: repositories.NewAgentRunRepository(db)}, nil
}

// CreatePending creates a pending run record.
func (s *sqliteTMUXRunStore) CreatePending(ctx context.Context, runID, botName, runtimeType string) error {
	return s.repo.CreatePending(ctx, runID, botName, runtimeType)
}

// UpsertDone marks a run as done.
func (s *sqliteTMUXRunStore) UpsertDone(ctx context.Context, runID, botName, runtimeType string) error {
	return s.repo.UpsertDone(ctx, runID, botName, runtimeType)
}

// GetByRunID retrieves a run record by ID.
func (s *sqliteTMUXRunStore) GetByRunID(ctx context.Context, runID string) (tmuxRunRecord, error) {
	run, err := s.repo.GetByRunID(ctx, runID)
	if err != nil {
		return tmuxRunRecord{}, err
	}
	return tmuxRunRecord{
		RunID:  run.RunID,
		Status: run.Status,
	}, nil
}
