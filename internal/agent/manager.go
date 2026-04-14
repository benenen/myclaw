package agent

import (
	"context"
	"fmt"
	"sync"
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
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
	m.mu.Lock()
	session, ok := m.sessions[botID]
	if ok && session.Matches(spec) && session.State() != SessionStateBroken {
		m.mu.Unlock()
		return session, nil
	}
	m.mu.Unlock()

	driver, err := driverForSpec(spec)
	if err != nil {
		return nil, err
	}
	replacement, err := NewSession(ctx, driver, spec)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok = m.sessions[botID]
	if ok && session.Matches(spec) && session.State() != SessionStateBroken {
		return session, nil
	}
	m.sessions[botID] = replacement
	return replacement, nil
}

func driverForSpec(spec Spec) (Driver, error) {
	name := spec.Type
	if name == "" {
		name = "oneshot"
	}
	driver, ok := LookupDriver(name)
	if !ok {
		return nil, fmt.Errorf("unknown agent driver type: %s", name)
	}
	return driver, nil
}
