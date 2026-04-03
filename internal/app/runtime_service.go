package app

import (
	"context"

	"github.com/benenen/channel-plugin/internal/channel"
	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/security"
)

type RuntimeService struct {
	appKeys  domain.AppKeyRepository
	accounts domain.ChannelAccountRepository
	cipher   *security.Cipher
	provider channel.Provider
}

func NewRuntimeService(
	appKeys domain.AppKeyRepository,
	accounts domain.ChannelAccountRepository,
	cipher *security.Cipher,
	provider channel.Provider,
) *RuntimeService {
	return &RuntimeService{
		appKeys:  appKeys,
		accounts: accounts,
		cipher:   cipher,
		provider: provider,
	}
}

type RuntimeConfigResult struct {
	ChannelType      string
	ChannelAccountID string
	AccountUID       string
	RuntimeConfig    channel.RuntimeConfig
}

func (s *RuntimeService) GetByAppKey(ctx context.Context, appKey string) (RuntimeConfigResult, error) {
	hash := security.HashAppKey(appKey)
	key, err := s.appKeys.FindByHash(ctx, hash)
	if err != nil {
		if err == domain.ErrNotFound {
			return RuntimeConfigResult{}, ErrAppKeyNotFound
		}
		return RuntimeConfigResult{}, err
	}
	if key.Status != "active" {
		return RuntimeConfigResult{}, ErrAppKeyDisabled
	}

	account, err := s.accounts.GetByID(ctx, key.ChannelAccountID)
	if err != nil {
		return RuntimeConfigResult{}, err
	}

	plaintext, err := s.cipher.Decrypt(account.CredentialCiphertext)
	if err != nil {
		return RuntimeConfigResult{}, err
	}

	cfg, err := s.provider.BuildRuntimeConfig(ctx, channel.BuildRuntimeConfigRequest{
		AccountUID:        account.AccountUID,
		CredentialPayload: plaintext,
		CredentialVersion: account.CredentialVersion,
	})
	if err != nil {
		return RuntimeConfigResult{}, err
	}

	// Update last_used_at asynchronously is fine, but for simplicity do it inline
	s.appKeys.UpdateLastUsedAt(ctx, key.ID)

	return RuntimeConfigResult{
		ChannelType:      account.ChannelType,
		ChannelAccountID: account.ID,
		AccountUID:       account.AccountUID,
		RuntimeConfig:    cfg,
	}, nil
}
