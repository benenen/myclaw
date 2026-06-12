package orchestration

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

// ListUpserter is the read+write registry slice the sweeper needs.
type ListUpserter interface {
	List(ctx context.Context) ([]domain.RegisteredAgent, error)
	Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error)
}

// SweepStaleAgents marks healthy remote agents whose heartbeat is older than ttl
// as unhealthy. Returns the number marked. Local agents are never swept.
func SweepStaleAgents(ctx context.Context, reg ListUpserter, ttl time.Duration, now func() time.Time) (int, error) {
	agents, err := reg.List(ctx)
	if err != nil {
		return 0, err
	}
	marked := 0
	for _, a := range agents {
		if a.Kind != domain.RegisteredAgentKindRemote || a.Health != domain.RegisteredAgentHealthy {
			continue
		}
		if a.LastHeartbeat == nil || now().Sub(*a.LastHeartbeat) <= ttl {
			continue
		}
		a.Health = domain.RegisteredAgentUnhealthy
		if _, err := reg.Upsert(ctx, a); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// StartHealthSweeper runs SweepStaleAgents on an interval until ctx is done.
func StartHealthSweeper(ctx context.Context, reg ListUpserter, ttl, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = SweepStaleAgents(ctx, reg, ttl, func() time.Time { return time.Now().UTC() })
			}
		}
	}()
}
