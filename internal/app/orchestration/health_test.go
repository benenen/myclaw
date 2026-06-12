package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type listUpsertRegistry struct {
	agents  []domain.RegisteredAgent
	updated []domain.RegisteredAgent
}

func (r *listUpsertRegistry) List(ctx context.Context) ([]domain.RegisteredAgent, error) {
	return r.agents, nil
}
func (r *listUpsertRegistry) Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error) {
	r.updated = append(r.updated, a)
	return a, nil
}

func TestSweepStaleRemoteAgents(t *testing.T) {
	old := time.Now().UTC().Add(-10 * time.Minute)
	fresh := time.Now().UTC()
	reg := &listUpsertRegistry{agents: []domain.RegisteredAgent{
		{Name: "stale", Kind: domain.RegisteredAgentKindRemote, Health: domain.RegisteredAgentHealthy, LastHeartbeat: &old},
		{Name: "ok", Kind: domain.RegisteredAgentKindRemote, Health: domain.RegisteredAgentHealthy, LastHeartbeat: &fresh},
		{Name: "local", Kind: domain.RegisteredAgentKindLocal, Health: domain.RegisteredAgentHealthy},
	}}

	n, err := SweepStaleAgents(context.Background(), reg, 5*time.Minute, func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 || len(reg.updated) != 1 || reg.updated[0].Name != "stale" || reg.updated[0].Health != domain.RegisteredAgentUnhealthy {
		t.Fatalf("expected only stale marked unhealthy, got n=%d updated=%+v", n, reg.updated)
	}
}
