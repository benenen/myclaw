package agent

import (
	"context"
	"reflect"
	"sync"
)

type Session struct {
	mu      sync.Mutex
	state   SessionState
	runtime SessionRuntime
	spec    Spec
}

func NewSession(ctx context.Context, driver Driver, spec Spec) (*Session, error) {
	clonedSpec := cloneSpec(spec)
	runtime, err := driver.Init(ctx, clonedSpec)
	if err != nil {
		return nil, err
	}
	return &Session{
		state:   SessionStateReady,
		runtime: runtime,
		spec:    clonedSpec,
	}, nil
}

func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) Send(ctx context.Context, req Request) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = SessionStateBusy
	resp, err := s.runtime.Run(ctx, req)
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
