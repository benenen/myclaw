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

func TestRegisteredAgentGetByIDFound(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	ctx := context.Background()

	in := domain.RegisteredAgent{
		ID:          domain.NewPrefixedID("ra"),
		Name:        "searcher",
		Description: "search agent",
		Kind:        domain.RegisteredAgentKindRemote,
		BotID:       "bot_2",
		Health:      domain.RegisteredAgentHealthy,
	}
	got, err := repo.Upsert(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	found, err := repo.GetByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if found.Name != in.Name || found.Kind != in.Kind || found.BotID != in.BotID {
		t.Fatalf("unexpected agent: %+v", found)
	}
}

func TestRegisteredAgentGetByIDNotFound(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	if _, err := repo.GetByID(context.Background(), "ra_does_not_exist"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRegisteredAgentDeleteByID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewRegisteredAgentRepository(db)
	ctx := context.Background()

	in := domain.RegisteredAgent{
		ID:      domain.NewPrefixedID("ra"),
		Name:    "deletable",
		Kind:    domain.RegisteredAgentKindLocal,
		BotID:   "bot_3",
		Health:  domain.RegisteredAgentHealthy,
	}
	got, err := repo.Upsert(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := repo.DeleteByID(ctx, got.ID); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if _, err := repo.GetByID(ctx, got.ID); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	// deleting an unknown id returns ErrNotFound
	if err := repo.DeleteByID(ctx, "ra_unknown"); err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound for unknown id, got %v", err)
	}
}
