package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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

type sessionPaneLister interface {
	ListPanes() ([]*gotmux.Pane, error)
}

var tmuxDiagnosticsCollector = collectTMUXDiagnostics

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
func (GotmuxFactory) Start(ctx context.Context, spec agent.Spec, sessionName string) (Session, Pane, bool, error) {
	if ctx.Err() != nil {
		return nil, nil, false, ctx.Err()
	}
	if len(spec.Args) > 0 {
		return nil, nil, false, fmt.Errorf("tmux driver does not support startup args yet")
	}
	if len(spec.Env) > 0 {
		return nil, nil, false, fmt.Errorf("tmux driver does not support startup env yet")
	}
	if err := validateStartupSpec(spec); err != nil {
		return nil, nil, false, wrapStartError(sessionName, spec, err)
	}

	tmux, err := gotmux.DefaultTmux()
	if err != nil {
		return nil, nil, false, err
	}

	var session *gotmux.Session
	created := false
	if tmux.HasSession(sessionName) {
		if existing, err := tmux.GetSessionByName(sessionName); err == nil && existing != nil {
			session = existing
		}
	} else {
		if err := startSession(ctx, sessionName, spec); err != nil {
			return nil, nil, false, wrapStartError(sessionName, spec, err)
		}
		created = true
		if createdSession, err := tmux.GetSessionByName(sessionName); err == nil && createdSession != nil {
			session = createdSession
		}
	}

	if session == nil {
		return nil, nil, false, fmt.Errorf("failed to create or find tmux session %q", sessionName)
	}

	pane, err := firstSessionPane(session, sessionName)
	if err != nil {
		err = withTMUXDiagnostics(err, tmuxDiagnosticsCollector(ctx, sessionName))
		_ = session.Kill()
		return nil, nil, created, wrapStartError(sessionName, spec, err)
	}
	return GotmuxSession{session: session}, GotmuxPane{pane: pane}, created, nil
}

func validateStartupSpec(spec agent.Spec) error {
	var issues []string

	commandPath := startupCommandPath(spec.Command)
	switch {
	case commandPath == "":
		issues = append(issues, "tmux command is empty")
	case strings.Contains(commandPath, "/"):
		if _, err := os.Stat(commandPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				issues = append(issues, fmt.Sprintf("tmux executable %q not found", commandPath))
			} else {
				issues = append(issues, fmt.Sprintf("tmux executable %q stat failed: %v", commandPath, err))
			}
		}
	default:
		if _, err := exec.LookPath(commandPath); err != nil {
			issues = append(issues, fmt.Sprintf("tmux executable %q not found in PATH", commandPath))
		}
	}

	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		if info, err := os.Stat(workDir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				issues = append(issues, fmt.Sprintf("tmux workdir %q does not exist", workDir))
			} else {
				issues = append(issues, fmt.Sprintf("tmux workdir %q stat failed: %v", workDir, err))
			}
		} else if !info.IsDir() {
			issues = append(issues, fmt.Sprintf("tmux workdir %q is not a directory", workDir))
		}
	}

	if len(issues) == 0 {
		return nil
	}
	return errors.New(strings.Join(issues, "; "))
}

func startupCommandPath(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func wrapStartError(sessionName string, spec agent.Spec, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("start tmux session %q: %v (command=%q workdir=%q)", sessionName, err, spec.Command, spec.WorkDir)
}

func withTMUXDiagnostics(err error, diagnostics string) error {
	if err == nil || strings.TrimSpace(diagnostics) == "" {
		return err
	}
	return fmt.Errorf("%v; tmux_diag=%s", err, diagnostics)
}

func startSession(ctx context.Context, sessionName string, spec agent.Spec) error {
	args := newSessionArgs(sessionName, spec)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, text)
	}
	return nil
}

func newSessionArgs(sessionName string, spec agent.Spec) []string {
	args := []string{"new-session", "-d"}
	if sessionName != "" {
		args = append(args, "-s", sessionName)
	}
	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		args = append(args, "-c", workDir)
	}
	if command := strings.TrimSpace(spec.Command); command != "" {
		args = append(args, command)
	}
	return args
}

func collectTMUXDiagnostics(ctx context.Context, sessionName string) string {
	if strings.TrimSpace(sessionName) == "" {
		return ""
	}

	diagCtx := ctx
	if diagCtx == nil {
		diagCtx = context.Background()
	}
	if _, hasDeadline := diagCtx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		diagCtx, cancel = context.WithTimeout(diagCtx, 2*time.Second)
		defer cancel()
	}

	commands := []struct {
		label string
		args  []string
	}{
		{label: "has-session", args: []string{"has-session", "-t", sessionName}},
		{label: "list-sessions", args: []string{"list-sessions"}},
		{label: "list-windows", args: []string{"list-windows", "-t", sessionName}},
		{label: "list-panes", args: []string{"list-panes", "-s", "-t", sessionName}},
	}

	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		cmd := exec.CommandContext(diagCtx, "tmux", command.args...)
		output, err := cmd.CombinedOutput()
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = "<empty>"
		}
		if err != nil {
			parts = append(parts, fmt.Sprintf("%s err=%q out=%q", command.label, err.Error(), text))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s out=%q", command.label, text))
	}
	return strings.Join(parts, "; ")
}

func firstSessionPane(session sessionPaneLister, sessionName string) (*gotmux.Pane, error) {
	panes, err := session.ListPanes()
	if err != nil {
		return nil, fmt.Errorf("start tmux session %q: %w", sessionName, err)
	}
	if len(panes) == 0 {
		return nil, fmt.Errorf("start tmux session %q: tmux session %q has no panes", sessionName, sessionName)
	}
	return panes[0], nil
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
