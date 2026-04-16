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
