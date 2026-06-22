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
