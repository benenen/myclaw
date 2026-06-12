package repositories

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/testutil"
)

func TestRegisteredAgentUpsertByName(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	ctx := context.Background()

	in := domain.RegisteredAgent{
		ID:          domain.NewPrefixedID("ra"),
		Name:        "researcher",
		Description: "web research",
		Kind:        domain.RegisteredAgentKindLocal,
		BotID:       "bot_1",
		Health:      domain.RegisteredAgentHealthy,
	}
	got, err := repo.Upsert(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.Name != "researcher" || got.Kind != "local" || got.BotID != "bot_1" {
		t.Fatalf("unexpected stored agent: %+v", got)
	}

	// upsert again by same name updates description
	in.Description = "deep web research"
	if _, err := repo.Upsert(ctx, in); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	byName, err := repo.GetByName(ctx, "researcher")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.Description != "deep web research" {
		t.Fatalf("description not updated: %+v", byName)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(all))
	}
}

func TestRegisteredAgentGetByNameNotFound(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	if _, err := repo.GetByName(context.Background(), "nope"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
