package app

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/store/repositories"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func TestListAccountsIncludesHasActiveAppKey(t *testing.T) {
	db := testutil.OpenTestDB(t)
	userRepo := repositories.NewUserRepository(db)
	accountRepo := repositories.NewChannelAccountRepository(db)
	appKeyRepo := repositories.NewAppKeyRepository(db)

	ctx := context.Background()

	user, _ := userRepo.FindOrCreateByExternalUserID(ctx, "u_123")
	accountRepo.Upsert(ctx, domain.ChannelAccount{
		ID: "acct_1", UserID: user.ID, ChannelType: "wechat", AccountUID: "wxid_1",
		DisplayName: "Test",
	})

	// Create an app key for the account
	appKeySvc := NewAppKeyService(appKeyRepo, accountRepo)
	appKeySvc.CreateOrRotate(ctx, "acct_1")

	svc := NewChannelAccountQueryService(userRepo, accountRepo, appKeyRepo)
	items, err := svc.ListByExternalUserID(ctx, "u_123", "wechat")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 account, got %d", len(items))
	}
	if !items[0].HasActiveAppKey {
		t.Fatal("expected has_active_app_key=true")
	}
}
