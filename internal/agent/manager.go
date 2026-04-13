package agent

import (
	"context"
	"sync"
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	driver   Driver
}

func NewManager(driver Driver) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		driver:   driver,
	}
}

func (m *Manager) Send(ctx context.Context, botID string, spec Spec, req Request) (Response, error) {
	session := m.sessionFor(botID, spec)
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

func (m *Manager) sessionFor(botID string, spec Spec) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[botID]
	if !ok {
		session = NewSession(m.driver, spec)
		m.sessions[botID] = session
		return session
	}

	if session.Matches(spec) && session.State() != SessionStateBroken {
		return session
	}

	replacement := NewSession(m.driver, spec)
	m.sessions[botID] = replacement
	return replacement
}
