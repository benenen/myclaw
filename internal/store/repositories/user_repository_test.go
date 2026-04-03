package repositories

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/testutil"
)

func TestUserRepositoryFindOrCreateByExternalUserID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewUserRepository(db)
	ctx := context.Background()

	user1, err := repo.FindOrCreateByExternalUserID(ctx, "u_123")
	if err != nil {
		t.Fatal(err)
	}
	user2, err := repo.FindOrCreateByExternalUserID(ctx, "u_123")
	if err != nil {
		t.Fatal(err)
	}
	if user1.ID != user2.ID {
		t.Fatal("expected idempotent user resolution")
	}
}
