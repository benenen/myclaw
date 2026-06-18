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
}

type Request struct {
	BotID     string
	UserID    string
	MessageID string
	Prompt    string
	WorkDir   string
	Metadata  map[string]string
}

type Response struct {
	Text        string
	RuntimeType string
	ExitCode    int
	Duration    time.Duration
	RawOutput   string
}
