package repositories

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func TestChannelAccountRepositoryUpsert(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewChannelAccountRepository(db)
	ctx := context.Background()

	acct1, err := repo.Upsert(ctx, domain.ChannelAccount{
		ID:          "acct_1",
		UserID:      "usr_1",
		ChannelType: "wechat",
		AccountUID:  "wxid_abc",
		DisplayName: "Test User",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Upsert again with same unique key should update, not create duplicate
	acct2, err := repo.Upsert(ctx, domain.ChannelAccount{
		ID:          "acct_2",
		UserID:      "usr_1",
		ChannelType: "wechat",
		AccountUID:  "wxid_abc",
		DisplayName: "Updated User",
	})
	if err != nil {
		t.Fatal(err)
	}
	if acct1.ID != acct2.ID {
		t.Fatal("expected upsert to reuse existing record")
	}
	if acct2.DisplayName != "Updated User" {
		t.Fatal("expected display name to be updated")
	}
}

func TestChannelAccountRepositoryListByUserID(t *testing.T) {
	db := testutil.OpenTestDB(t)
	repo := NewChannelAccountRepository(db)
	ctx := context.Background()

	repo.Upsert(ctx, domain.ChannelAccount{
		ID: "acct_1", UserID: "usr_1", ChannelType: "wechat", AccountUID: "wxid_1",
	})
	repo.Upsert(ctx, domain.ChannelAccount{
		ID: "acct_2", UserID: "usr_1", ChannelType: "wechat", AccountUID: "wxid_2",
	})

	items, err := repo.ListByUserID(ctx, "usr_1", "wechat")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2, got %d", len(items))
	}
}
