package codex

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

type TMUXDriver struct {
	factory         tmuxRuntimeFactory
	runStoreFactory tmuxRunStoreFactory
}

type TMUXRuntime struct {
	mu    sync.Mutex
	runMu sync.Mutex

	state    runtimeState
	pane     tmux.Pane
	session  tmux.Session
	readErr  error
	waitGap  time.Duration
	spec     agent.Spec
	runStore tmuxRunStore
}

type tmuxRuntimeFactory interface {
	Start(ctx context.Context, spec agent.Spec, sessionName string) (tmux.Session, tmux.Pane, bool, error)
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

func init() {
	agent.MustRegisterDriver("codex-tmux", func() agent.Driver {
		return NewTMUXDriver()
	})
}

func NewTMUXDriver() *TMUXDriver {
	return &TMUXDriver{
		factory:         tmux.GotmuxFactory{},
		runStoreFactory: sqliteTMUXRunStoreFactory{},
	}
}

func (d *TMUXDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("codex tmux driver requires command")
	}
	if strings.TrimSpace(spec.WorkDir) == "" {
		return nil, fmt.Errorf("codex tmux driver requires workdir")
	}

	runtime := &TMUXRuntime{
		state:   stateStarting,
		waitGap: 10 * time.Millisecond,
		spec:    spec,
	}
	if d != nil {
		runtimeFactory := d.factory
		if runtimeFactory == nil {
			runtimeFactory = tmux.GotmuxFactory{}
		}
		startupSpec := spec
		startupSpec.Command = buildTMUXShellCommand(spec)
		sessionName := nextTMUXSessionName(spec.BotName)
		session, pane, created, err := runtimeFactory.Start(ctx, startupSpec, sessionName)
		if err != nil {
			return nil, err
		}
		runtime.session = session
		runtime.pane = pane

		runStoreFactory := d.runStoreFactory
		if runStoreFactory == nil {
			runStoreFactory = sqliteTMUXRunStoreFactory{}
		}
		runStore, err := runStoreFactory.Open(spec)
		if err != nil {
			return nil, err
		}
		runtime.runStore = runStore

		if !created {
			runtime.mu.Lock()
			runtime.state = stateReady
			runtime.mu.Unlock()
			tmuxInitLogf("codex tmux waitUntilReady skipped: bot=%s session=%s created=%t", spec.BotName, sessionName, created)
			return runtime, nil
		}
	}

	err := runtime.waitUntilReady(ctx)
	if err != nil {
		tmuxInitLogf("codex tmux waitUntilReady failed: bot=%s session=%s err=%v", spec.BotName, nextTMUXSessionName(spec.BotName), err)
		runtime.markBroken(err)
		return nil, err
	}
	tmuxInitLogf("codex tmux waitUntilReady ok: bot=%s session=%s", spec.BotName, nextTMUXSessionName(spec.BotName))
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
	r.state = stateRunning
	r.mu.Unlock()

	if pane == nil {
		r.markBroken(fmt.Errorf("codex tmux runtime is not connected to a pane"))
		return agent.Response{}, r.currentError()
	}
	if r.runStore == nil {
		r.markBroken(fmt.Errorf("codex tmux runtime is not connected to run state store"))
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
	if err := r.runStore.CreatePending(runCtx, runID, r.spec.BotName, runtimeTypeCodex); err != nil {
		r.markBroken(fmt.Errorf("codex tmux create run failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	if err := pane.SendKeys(promptText, "C-m"); err != nil {
		r.markBroken(fmt.Errorf("codex tmux send failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	if err := r.waitRunCompletion(runCtx, runID); err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	captured, err := pane.CapturePane()
	if err != nil {
		r.markBroken(fmt.Errorf("codex tmux capture failed: %w", err))
		return agent.Response{}, r.currentError()
	}
	text := tmux.CleanupTMUXRunText(tmux.NormalizeTMUXOutput(captured))

	r.mu.Lock()
	if r.state != stateBroken {
		r.state = stateReady
	}
	r.mu.Unlock()

	return agent.Response{Text: text, RuntimeType: runtimeTypeCodex, ExitCode: 0, RawOutput: text}, nil
}

func (r *TMUXRuntime) waitUntilReady(ctx context.Context) error {
	r.mu.Lock()
	if r.pane == nil {
		r.state = stateReady
		r.mu.Unlock()
		return nil
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
		normalized := tmux.NormalizeTMUXOutput(captured)
		if codexTMUXReady(normalized) {
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

func codexTMUXReady(text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "OpenAI Codex")
}

func (r *TMUXRuntime) waitRunCompletion(ctx context.Context, runID string) error {
	r.mu.Lock()
	gap := r.waitGap
	runStore := r.runStore
	r.mu.Unlock()
	if gap <= 0 {
		gap = 10 * time.Millisecond
	}

	for {
		run, err := runStore.GetByRunID(ctx, runID)
		if err == nil && run.Status == "done" {
			return nil
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("codex tmux run state read failed: %w", err)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("codex tmux run timed out: %w", ctx.Err())
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

func writeTMUXCurrentRunID(workDir, runID string) error {
	if strings.TrimSpace(workDir) == "" {
		return fmt.Errorf("codex tmux workdir is required")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("codex tmux prepare workdir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, currentTMUXRunIDFileName), []byte(runID+"\n"), 0o644); err != nil {
		return fmt.Errorf("codex tmux write current run id: %w", err)
	}
	return nil
}

type sqliteTMUXRunStore struct {
	repo *repositories.AgentRunRepository
}

func (sqliteTMUXRunStoreFactory) Open(spec agent.Spec) (tmuxRunStore, error) {
	if strings.TrimSpace(spec.SQLitePath) == "" {
		return nil, fmt.Errorf("codex tmux driver requires sqlite path")
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

func (s *sqliteTMUXRunStore) CreatePending(ctx context.Context, runID, botName, runtimeType string) error {
	return s.repo.CreatePending(ctx, runID, botName, runtimeType)
}

func (s *sqliteTMUXRunStore) UpsertDone(ctx context.Context, runID, botName, runtimeType string) error {
	return s.repo.UpsertDone(ctx, runID, botName, runtimeType)
}

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

func buildTMUXShellCommand(spec agent.Spec) string {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return ""
	}
	notifyCommand := "myclaw"
	if executable, err := currentExecutablePath(); err == nil && strings.TrimSpace(executable) != "" {
		notifyCommand = executable
	}
	notifyConfig := fmt.Sprintf(`notify=[%s, "notify", "codex", %s]`, strconv.Quote(notifyCommand), strconv.Quote(spec.BotName))
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

func nextTMUXSessionName(botName string) string {
	prefix := strings.TrimSpace(botName)
	prefix = strings.ToLower(prefix)
	prefix = strings.ReplaceAll(prefix, " ", "-")
	if prefix == "" {
		prefix = "codex"
	}
	return fmt.Sprintf("myclaw-codex-%s", prefix)
}
