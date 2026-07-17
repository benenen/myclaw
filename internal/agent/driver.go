package agent

import (
	"context"
	"fmt"
	"sync"
)

type Driver interface {
	Init(ctx context.Context, spec Spec) (SessionRuntime, error)
}

type SessionRuntime interface {
	Run(ctx context.Context, req Request) (Response, error)
	Close() error
}

type DriverFactory func() Driver

type RuntimeState string

const (
	StateStarting RuntimeState = "starting"
	StateReady    RuntimeState = "ready"
	StateRunning  RuntimeState = "running"
	StateBroken   RuntimeState = "broken"
)

func FlattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

var (
	driversMu sync.RWMutex
	drivers   = map[string]DriverFactory{}
)

func LookupDriver(name string) (Driver, bool) {
	driversMu.RLock()
	factory, ok := drivers[name]
	driversMu.RUnlock()
	if !ok {
		return nil, false
	}
	return factory(), true
}

func RegisterDriver(name string, factory DriverFactory) error {
	driversMu.Lock()
	defer driversMu.Unlock()
	if _, exists := drivers[name]; exists {
		return fmt.Errorf("driver already registered: %s", name)
	}
	drivers[name] = factory
	return nil
}

func MustRegisterDriver(name string, factory DriverFactory) {
	if err := RegisterDriver(name, factory); err != nil {
		panic(err)
	}
}
