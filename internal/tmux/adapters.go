package tmux

import (
	"context"

	"github.com/benenen/myclaw/internal/agent"
)

// Pane represents a tmux pane interface for sending keys and capturing output.
type Pane interface {
	SendKeys(keys ...string) error
	CapturePane() (string, error)
}

// Session represents a tmux session interface for lifecycle management.
type Session interface {
	Kill() error
}

// Factory represents a tmux runtime factory interface for creating sessions and panes.
type Factory interface {
	Start(ctx context.Context, spec agent.Spec, sessionName string) (Session, Pane, error)
}
