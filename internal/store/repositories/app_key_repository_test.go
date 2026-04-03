package repositories

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func TestAppKeyRepositoryCreateAndFindByHash(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewAppKeyRepository(db)
	ctx := context.Background()

	_, err := repo.Create(ctx, domain.AppKey{
		ID:               "key_1",
		UserID:           "usr_1",
		ChannelAccountID: "acct_1",
		AppKeyHash:       "hash_abc",
		AppKeyPrefix:     "appk_abc",
		Status:           "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := repo.FindByHash(ctx, "hash_abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "key_1" {
		t.Fatalf("unexpected key: %s", got.ID)
	}
}

func TestAppKeyRepositoryDisableByChannelAccountID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewAppKeyRepository(db)
	ctx := context.Background()

	repo.Create(ctx, domain.AppKey{
		ID: "key_1", UserID: "usr_1", ChannelAccountID: "acct_1",
		AppKeyHash: "h1", AppKeyPrefix: "appk_xxx", Status: "active",
	})

	if err := repo.DisableByChannelAccountID(ctx, "acct_1"); err != nil {
		t.Fatal(err)
	}

	has, _ := repo.HasActiveByChannelAccountID(ctx, "acct_1")
	if has {
		t.Fatal("expected no active key after disable")
	}
}
