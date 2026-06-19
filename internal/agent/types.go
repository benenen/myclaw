package agent

import "time"

type SessionState string

const (
	SessionStateStarting SessionState = "starting"
	SessionStateReady    SessionState = "ready"
	SessionStateBusy     SessionState = "busy"
	SessionStateBroken   SessionState = "broken"
	SessionStateStopped  SessionState = "stopped"
)

type Spec struct {
	BotID      string
	BotName    string
	Type       string
	Command    string
	Args       []string
	WorkDir    string
	SQLitePath string
	Env        map[string]string
	Timeout    time.Duration
	QueueSize  int
	// Orchestrator marks a brain session: the orchestrator replies with an
	// immediate ack and runs the turn detached, pushing the final answer later.
	Orchestrator bool
	// RealCLI marks the command as the genuine target CLI even when its
	// basename isn't the canonical name (e.g. an operator alias like "cx"
	// for codex), so drivers still inject real-binary args.
	RealCLI bool
	// ResumeSessionID, when non-empty, asks the driver to resume that prior CLI
	// session instead of starting fresh. Best-effort: drivers fall back to a new
	// session if the CLI rejects it.
	ResumeSessionID string
}

type Request struct {
	BotID     string
	UserID    string
	MessageID string
	Prompt    string
	WorkDir   string
	Metadata  map[string]string
	// OnProgress, when non-nil, is invoked by the driver as it parses
	// intermediate tool events during a turn. Nil = no tracing (default).
	OnProgress func(ProgressEvent)
}

type Response struct {
	Text        string
	RuntimeType string
	ExitCode    int
	Duration    time.Duration
	RawOutput   string
	// SessionID is the CLI's native session id for this turn, surfaced so the
	// orchestrator can persist it per (bot, cli) for later resume.
	SessionID string
}
