package tmux

import (
	"context"
	"fmt"
	"strings"

	"github.com/GianlucaP106/gotmux/gotmux"
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

// GotmuxSession wraps a gotmux Session to implement the Session interface.
type GotmuxSession struct {
	session *gotmux.Session
}

// GotmuxPane wraps a gotmux Pane to implement the Pane interface.
type GotmuxPane struct {
	pane *gotmux.Pane
}

// GotmuxFactory creates gotmux-backed Session and Pane instances.
type GotmuxFactory struct{}

// Kill terminates the tmux session.
func (s GotmuxSession) Kill() error {
	if s.session == nil {
		return nil
	}
	if err := s.session.Kill(); err != nil {
		return fmt.Errorf("kill tmux session %q: %w", s.session.Name, err)
	}
	return nil
}

// SendKeys sends one or more key sequences to the tmux pane.
func (p GotmuxPane) SendKeys(keys ...string) error {
	if p.pane == nil {
		return fmt.Errorf("tmux pane is nil")
	}
	for _, key := range keys {
		if err := p.pane.SendKeys(key); err != nil {
			return fmt.Errorf("send tmux keys: %w", err)
		}
	}
	return nil
}

// CapturePane captures the current content of the tmux pane.
func (p GotmuxPane) CapturePane() (string, error) {
	if p.pane == nil {
		return "", fmt.Errorf("tmux pane is nil")
	}
	output, err := p.pane.Capture()
	if err != nil {
		return "", fmt.Errorf("capture tmux pane: %w", err)
	}
	return output, nil
}

// Start creates a new tmux session or reuses an existing one.
func (GotmuxFactory) Start(ctx context.Context, spec agent.Spec, sessionName string) (Session, Pane, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if len(spec.Args) > 0 {
		return nil, nil, fmt.Errorf("tmux driver does not support startup args yet")
	}
	if len(spec.Env) > 0 {
		return nil, nil, fmt.Errorf("tmux driver does not support startup env yet")
	}

	tmux, err := gotmux.DefaultTmux()
	if err != nil {
		return nil, nil, err
	}

	var session *gotmux.Session
	if tmux.HasSession(sessionName) {
		if existing, err := tmux.GetSessionByName(sessionName); err == nil && existing != nil {
			session = existing
		}
	} else {
		options := &gotmux.SessionOptions{
			Name:         sessionName,
			ShellCommand: spec.Command,
		}
		if strings.TrimSpace(spec.WorkDir) != "" {
			options.StartDirectory = spec.WorkDir
		}
		session, err = tmux.NewSession(options)
		if err != nil {
			return nil, nil, fmt.Errorf("start tmux session %q: %w", sessionName, err)
		}
	}

	if session == nil {
		return nil, nil, fmt.Errorf("failed to create or find tmux session %q", sessionName)
	}

	window, err := session.GetWindowByIndex(0)
	if err != nil {
		_ = session.Kill()
		return nil, nil, fmt.Errorf("start tmux session %q: %w", sessionName, err)
	}
	if window == nil {
		_ = session.Kill()
		return nil, nil, fmt.Errorf("tmux session %q has no window at index 0", sessionName)
	}
	panes, err := window.ListPanes()
	if err != nil || len(panes) == 0 {
		_ = session.Kill()
		if err == nil {
			err = fmt.Errorf("tmux session %q has no panes", sessionName)
		}
		return nil, nil, fmt.Errorf("start tmux session %q: %w", sessionName, err)
	}
	pane := GotmuxPane{pane: panes[0]}
	return GotmuxSession{session: session}, pane, nil
}

// NormalizeTMUXOutput normalizes line endings in tmux output.
func NormalizeTMUXOutput(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// CleanupTMUXRunText removes empty lines and trailing carriage returns from tmux output.
func CleanupTMUXRunText(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, strings.TrimRight(line, "\r"))
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// ShellQuote quotes a string for safe use in shell commands.
func ShellQuote(text string) string {
	if text == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
