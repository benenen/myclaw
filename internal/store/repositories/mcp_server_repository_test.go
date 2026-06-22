package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/testutil"
)

func newMCPRepo(t *testing.T) *MCPServerRepository {
	t.Helper()
	db := testutil.OpenTestDB(t)
	return NewMCPServerRepository(db)
}

func TestMCPServerCRUD(t *testing.T) {
	repo := newMCPRepo(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, domain.MCPServer{
		ID: "mcp_1", Name: "fs", ServerType: "stdio",
		Command: "npx", Args: []string{"-y", "server"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "fs" || len(created.Args) != 2 {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := repo.GetByName(ctx, "fs")
	if err != nil || got.ID != "mcp_1" {
		t.Fatalf("getByName: %+v err=%v", got, err)
	}

	got.Enabled = false
	if _, err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, _ := repo.GetByID(ctx, "mcp_1")
	if again.Enabled {
		t.Fatalf("expected disabled after update")
	}

	if err := repo.DeleteByID(ctx, "mcp_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, "mcp_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMCPServerListOrdersByName(t *testing.T) {
	repo := newMCPRepo(t)
	ctx := context.Background()
	if _, err := repo.Create(ctx, domain.MCPServer{ID: "mcp_b", Name: "bravo", ServerType: "http", URL: "http://b", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, domain.MCPServer{ID: "mcp_a", Name: "alpha", ServerType: "http", URL: "http://a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "bravo" {
		t.Fatalf("expected [alpha bravo], got %+v", list)
	}
}

func TestMCPServerBotAttachment(t *testing.T) {
	repo := newMCPRepo(t)
	ctx := context.Background()

	a, _ := repo.Create(ctx, domain.MCPServer{ID: "mcp_a", Name: "a", ServerType: "http", URL: "http://a", Enabled: true})
	b, _ := repo.Create(ctx, domain.MCPServer{ID: "mcp_b", Name: "b", ServerType: "http", URL: "http://b", Enabled: false})

	// attach is idempotent
	if err := repo.AttachToBot(ctx, "bot_1", a.ID); err != nil {
		t.Fatalf("attach a: %v", err)
	}
	if err := repo.AttachToBot(ctx, "bot_1", a.ID); err != nil {
		t.Fatalf("re-attach a: %v", err)
	}
	if err := repo.AttachToBot(ctx, "bot_1", b.ID); err != nil {
		t.Fatalf("attach b: %v", err)
	}

	all, err := repo.ListByBot(ctx, "bot_1")
	if err != nil {
		t.Fatalf("list by bot: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListByBot = %d, want 2", len(all))
	}
	enabled, err := repo.ListEnabledByBot(ctx, "bot_1")
	if err != nil {
		t.Fatalf("list enabled by bot: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != "mcp_a" {
		t.Fatalf("ListEnabledByBot = %+v, want only mcp_a", enabled)
	}

	// replace-set
	if err := repo.SetBotServers(ctx, "bot_1", []string{b.ID}); err != nil {
		t.Fatalf("set: %v", err)
	}
	all, err = repo.ListByBot(ctx, "bot_1")
	if err != nil {
		t.Fatalf("list after set: %v", err)
	}
	if len(all) != 1 || all[0].ID != "mcp_b" {
		t.Fatalf("after set ListByBot = %+v, want only mcp_b", all)
	}

	// empty set clears all attachments
	if err := repo.SetBotServers(ctx, "bot_1", []string{}); err != nil {
		t.Fatalf("clear set: %v", err)
	}
	cleared, err := repo.ListByBot(ctx, "bot_1")
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("empty SetBotServers did not clear: %+v", cleared)
	}

	// detach — re-attach b first so there is something to detach
	if err := repo.AttachToBot(ctx, "bot_1", b.ID); err != nil {
		t.Fatalf("re-attach b before detach: %v", err)
	}
	if err := repo.DetachFromBot(ctx, "bot_1", b.ID); err != nil {
		t.Fatalf("detach: %v", err)
	}
	all, err = repo.ListByBot(ctx, "bot_1")
	if err != nil {
		t.Fatalf("list after detach: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("after detach = %d, want 0", len(all))
	}

	// delete cascades join rows
	if err := repo.AttachToBot(ctx, "bot_2", a.ID); err != nil {
		t.Fatalf("attach a to bot_2: %v", err)
	}
	if err := repo.DeleteByID(ctx, a.ID); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	leftover, err := repo.ListByBot(ctx, "bot_2")
	if err != nil {
		t.Fatalf("list bot_2 after cascade: %v", err)
	}
	if len(leftover) != 0 {
		t.Fatalf("delete did not cascade: %+v", leftover)
	}
}
