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
	Type      string
	Command   string
	Args      []string
	WorkDir   string
	Env       map[string]string
	Timeout   time.Duration
	QueueSize int
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
	Text      string
	ExitCode  int
	Duration  time.Duration
	RawOutput string
}
