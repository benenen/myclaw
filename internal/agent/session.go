package agent

import (
	"context"
	"reflect"
	"sync"
)

type Driver interface {
	Run(ctx context.Context, spec Spec, req Request) (Response, error)
}

type Session struct {
	mu     sync.Mutex
	state  SessionState
	driver Driver
	spec   Spec
}

func NewSession(driver Driver, spec Spec) *Session {
	return &Session{
		state:  SessionStateReady,
		driver: driver,
		spec:   cloneSpec(spec),
	}
}

func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) Send(ctx context.Context, req Request) (Response, error) {
	s.mu.Lock()
	s.state = SessionStateBusy
	spec := cloneSpec(s.spec)
	s.mu.Unlock()

	resp, err := s.driver.Run(ctx, spec, req)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state = SessionStateBroken
		return resp, err
	}

	s.state = SessionStateReady
	return resp, nil
}

func (s *Session) Matches(spec Spec) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return reflect.DeepEqual(s.spec, spec)
}

func cloneSpec(spec Spec) Spec {
	cloned := spec
	if spec.Args != nil {
		cloned.Args = append([]string(nil), spec.Args...)
	}
	if spec.Env != nil {
		cloned.Env = make(map[string]string, len(spec.Env))
		for k, v := range spec.Env {
			cloned.Env[k] = v
		}
	}
	return cloned
}
