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
