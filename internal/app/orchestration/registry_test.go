package orchestration

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

type fakeBotLister struct{ bots []domain.Bot }

func (f fakeBotLister) ListWithAccounts(ctx context.Context) ([]domain.Bot, error) {
	return f.bots, nil
}

type recordingRegistry struct{ upserts []domain.RegisteredAgent }

func (r *recordingRegistry) Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error) {
	r.upserts = append(r.upserts, a)
	return a, nil
}

func TestSyncLocalAgentsRegistersSubagents(t *testing.T) {
	bots := fakeBotLister{bots: []domain.Bot{
		{ID: "bot_a", Name: "researcher", Type: domain.BotTypeSubagent},
		{ID: "bot_b", Name: "channel-bot", Type: "channel"},
	}}
	reg := &recordingRegistry{}

	if err := SyncLocalAgents(context.Background(), bots, reg); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(reg.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(reg.upserts))
	}
	got := reg.upserts[0]
	if got.Name != "researcher" || got.Kind != domain.RegisteredAgentKindLocal || got.BotID != "bot_a" {
		t.Fatalf("unexpected upsert: %+v", got)
	}
}
