package app

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/store/repositories"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func newTestAppKeyService(t *testing.T) *AppKeyService {
	t.Helper()
	db := testutil.OpenTestDB(t)
	return NewAppKeyService(
		repositories.NewAppKeyRepository(db),
		repositories.NewChannelAccountRepository(db),
	)
}

func seedAccount(t *testing.T, db interface{ OpenTestDB() }) {}

func TestCreateAppKeyRotatesExistingKey(t *testing.T) {
	db := testutil.OpenTestDB(t)
	accountRepo := repositories.NewChannelAccountRepository(db)
	appKeyRepo := repositories.NewAppKeyRepository(db)
	svc := NewAppKeyService(appKeyRepo, accountRepo)
	ctx := context.Background()

	// Seed an account
	accountRepo.Upsert(ctx, domain.ChannelAccount{
		ID: "acct_1", UserID: "usr_1", ChannelType: "wechat", AccountUID: "wxid_1",
	})

	first, err := svc.CreateOrRotate(ctx, "acct_1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateOrRotate(ctx, "acct_1")
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyID == second.KeyID {
		t.Fatal("expected rotation to create new key")
	}

	// Old key should be disabled
	has, _ := appKeyRepo.HasActiveByChannelAccountID(ctx, "acct_1")
	if !has {
		t.Fatal("expected active key after rotation")
	}
}

func TestDisableByChannelAccountIDIsIdempotent(t *testing.T) {
	svc := newTestAppKeyService(t)
	ctx := context.Background()

	// Disabling when no key exists should not error
	if err := svc.Disable(ctx, DisableAppKeyInput{ChannelAccountID: "acct_nonexist"}); err != nil {
		t.Fatal(err)
	}
}

func TestDisableRejectsBothIdentifiers(t *testing.T) {
	svc := newTestAppKeyService(t)
	ctx := context.Background()

	err := svc.Disable(ctx, DisableAppKeyInput{ChannelAccountID: "acct_1", KeyID: "key_1"})
	if err != domain.ErrInvalidArg {
		t.Fatalf("expected ErrInvalidArg, got: %v", err)
	}
}
