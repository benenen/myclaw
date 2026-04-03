package app

import (
	"context"
	"testing"

	"github.com/benenen/channel-plugin/internal/channel/wechat"
	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/security"
	"github.com/benenen/channel-plugin/internal/store/repositories"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func TestRuntimeServiceReturnsConfigForValidAppKey(t *testing.T) {
	db := testutil.OpenTestDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, _ := security.NewCipher(key)
	provider := wechat.NewFakeProvider()
	accountRepo := repositories.NewChannelAccountRepository(db)
	appKeyRepo := repositories.NewAppKeyRepository(db)

	ctx := context.Background()

	// Seed account with encrypted credentials
	cred := []byte(`{"session":"test"}`)
	encrypted, _ := cipher.Encrypt(cred)
	accountRepo.Upsert(ctx, domain.ChannelAccount{
		ID:                   "acct_1",
		UserID:               "usr_1",
		ChannelType:          "wechat",
		AccountUID:           "wxid_1",
		CredentialCiphertext: encrypted,
		CredentialVersion:    1,
	})

	// Create app key
	appKeySvc := NewAppKeyService(appKeyRepo, accountRepo)
	keyResult, _ := appKeySvc.CreateOrRotate(ctx, "acct_1")

	runtimeSvc := NewRuntimeService(appKeyRepo, accountRepo, cipher, provider)
	cfg, err := runtimeSvc.GetByAppKey(ctx, keyResult.AppKey)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChannelType != "wechat" {
		t.Fatalf("unexpected channel type: %s", cfg.ChannelType)
	}
	if cfg.RuntimeConfig == nil {
		t.Fatal("expected runtime config")
	}
}

func TestRuntimeServiceReturnsErrorForUnknownKey(t *testing.T) {
	db := testutil.OpenTestDB(t)
	key := make([]byte, 32)
	cipher, _ := security.NewCipher(key)
	provider := wechat.NewFakeProvider()
	appKeyRepo := repositories.NewAppKeyRepository(db)
	accountRepo := repositories.NewChannelAccountRepository(db)

	svc := NewRuntimeService(appKeyRepo, accountRepo, cipher, provider)
	_, err := svc.GetByAppKey(context.Background(), "appk_nonexistent")
	if err != ErrAppKeyNotFound {
		t.Fatalf("expected ErrAppKeyNotFound, got: %v", err)
	}
}
