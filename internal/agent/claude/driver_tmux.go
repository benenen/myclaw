package claude

import (
	"context"
	_ "errors"
	_ "fmt"
	_ "os"
	_ "path/filepath"
	_ "strconv"
	_ "strings"
	"sync"
	"time"

	"github.com/GianlucaP106/gotmux/gotmux"
	"github.com/benenen/myclaw/internal/agent"
	_ "github.com/benenen/myclaw/internal/domain"
	_ "github.com/benenen/myclaw/internal/store"
	_ "github.com/benenen/myclaw/internal/store/repositories"
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
