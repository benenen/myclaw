package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	sinks    map[string]PushSink
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		sinks:    make(map[string]PushSink),
	}
}

// SetPushSink registers the bot's receiver for scheduled-task responses. The
// sink is kept per bot and re-injected whenever the bot's session is created
// or replaced, so it survives session recreation.
func (m *Manager) SetPushSink(botID string, sink PushSink) {
	m.mu.Lock()
	m.sinks[botID] = sink
	session := m.sessions[botID]
	m.mu.Unlock()
	if session != nil {
		session.SetPushSink(sink)
	}
}

// Schedule registers a periodic task on the bot's existing session. Scheduling
// attaches to the already-running agent: it never creates or replaces a session
// (that path locks the turn mutex via sessionFor/Matches/State and would
// deadlock a schedule_task call the agent makes from inside its own turn). It
// returns an error when the bot has no live session.
func (m *Manager) Schedule(botID string, task ScheduledTask) (string, error) {
	m.mu.Lock()
	session := m.sessions[botID]
	m.mu.Unlock()
	if session == nil {
		return "", fmt.Errorf("no active session for bot %s", botID)
	}
	return session.Schedule(task)
}

// CancelTask cancels one scheduled task. It returns false when the bot has no
// session or the task is unknown.
func (m *Manager) CancelTask(botID, taskID string) bool {
	m.mu.Lock()
	session := m.sessions[botID]
	m.mu.Unlock()
	if session == nil {
		return false
	}
	return session.CancelTask(taskID)
}

// Tasks lists the bot's active scheduled tasks.
func (m *Manager) Tasks(botID string) []ScheduledTask {
	m.mu.Lock()
	session := m.sessions[botID]
	m.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Tasks()
}

// StopBot closes and removes the bot's session, stopping its scheduled tasks
// and releasing the CLI process. Bound to the bot's operational life: call it
// when the bot disconnects/stops. No-op for unknown bots.
func (m *Manager) StopBot(botID string) {
	m.mu.Lock()
	session := m.sessions[botID]
	delete(m.sessions, botID)
	delete(m.sinks, botID)
	m.mu.Unlock()
	if session == nil {
		return
	}
	if err := session.Close(); err != nil {
		slog.Error("bot session close failed", "bot_id", botID, "error", err)
	}
}

func (m *Manager) Send(ctx context.Context, botID string, spec Spec, req Request) (Response, error) {
	session, err := m.sessionFor(ctx, botID, spec)
	if err != nil {
		return Response{}, err
	}
	return session.Send(ctx, req)
}

func (m *Manager) State(botID string) SessionState {
	m.mu.Lock()
	session, ok := m.sessions[botID]
	m.mu.Unlock()
	if !ok {
		return SessionStateStopped
	}
	return session.State()
}

func (m *Manager) sessionFor(ctx context.Context, botID string, spec Spec) (*Session, error) {
	// Read the pointer under m.mu, then evaluate Matches/State (which lock the
	// session's turn mutex s.mu) with m.mu released. Holding m.mu across an
	// s.mu acquisition freezes the whole Manager whenever that session is
	// mid-turn (s.mu held for the turn's duration).
	m.mu.Lock()
	session := m.sessions[botID]
	m.mu.Unlock()
	if session != nil && session.Matches(spec) && session.State() != SessionStateBroken {
		return session, nil
	}

	driver, err := driverForSpec(spec)
	if err != nil {
		return nil, err
	}
	replacement, err := NewSession(context.Background(), driver, spec)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	current := m.sessions[botID]
	m.mu.Unlock()
	if current != nil && current.Matches(spec) && current.State() != SessionStateBroken {
		if err := replacement.Close(); err != nil {
			return nil, err
		}
		return current, nil
	}

	var stale *Session
	m.mu.Lock()
	stale = m.sessions[botID]
	m.sessions[botID] = replacement
	sink := m.sinks[botID]
	m.mu.Unlock()
	if sink != nil {
		replacement.SetPushSink(sink)
	}

	if stale != nil {
		if err := stale.Close(); err != nil {
			m.mu.Lock()
			if current, exists := m.sessions[botID]; exists && current == replacement {
				m.sessions[botID] = stale
			}
			m.mu.Unlock()
			_ = replacement.Close()
			return nil, err
		}
	}
	return replacement, nil
}

func driverForSpec(spec Spec) (Driver, error) {
	name := spec.Type
	if name == "" {
		name = "codex-exec"
	}
	driver, ok := LookupDriver(name)
	if !ok {
		return nil, fmt.Errorf("unknown agent driver type: %s", name)
	}
	return driver, nil
}
