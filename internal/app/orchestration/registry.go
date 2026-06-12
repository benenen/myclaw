package orchestration

import (
	"context"

	"github.com/benenen/myclaw/internal/domain"
)

// BotLister is the slice of the bot repo we need for auto-registration.
type BotLister interface {
	ListWithAccounts(ctx context.Context) ([]domain.Bot, error)
}

// Upserter is the write side of the registry.
type Upserter interface {
	Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error)
}

// SyncLocalAgents registers every Type=subagent bot as a local registry entry.
func SyncLocalAgents(ctx context.Context, bots BotLister, reg Upserter) error {
	all, err := bots.ListWithAccounts(ctx)
	if err != nil {
		return err
	}
	for _, b := range all {
		if b.Type != domain.BotTypeSubagent {
			continue
		}
		if _, err := reg.Upsert(ctx, domain.RegisteredAgent{
			ID:          domain.NewPrefixedID("ra"),
			Name:        b.Name,
			Description: b.Name,
			Kind:        domain.RegisteredAgentKindLocal,
			BotID:       b.ID,
			Health:      domain.RegisteredAgentHealthy,
		}); err != nil {
			return err
		}
	}
	return nil
}
