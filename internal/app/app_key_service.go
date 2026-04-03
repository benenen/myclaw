package app

import (
	"context"
	"errors"
	"time"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/security"
)

var (
	ErrAppKeyNotFound = errors.New("app key not found")
	ErrAppKeyDisabled = errors.New("app key disabled")
)

type AppKeyService struct {
	appKeys  domain.AppKeyRepository
	accounts domain.ChannelAccountRepository
}

func NewAppKeyService(appKeys domain.AppKeyRepository, accounts domain.ChannelAccountRepository) *AppKeyService {
	return &AppKeyService{appKeys: appKeys, accounts: accounts}
}

type AppKeyResult struct {
	KeyID        string
	AppKey       string
	AppKeyPrefix string
	CreatedAt    time.Time
}

func (s *AppKeyService) CreateOrRotate(ctx context.Context, channelAccountID string) (AppKeyResult, error) {
	// Verify account exists
	account, err := s.accounts.GetByID(ctx, channelAccountID)
	if err != nil {
		return AppKeyResult{}, err
	}

	// Disable existing active key
	s.appKeys.DisableByChannelAccountID(ctx, channelAccountID)

	// Generate new key
	plaintext, prefix, hash, err := security.GenerateAppKey()
	if err != nil {
		return AppKeyResult{}, err
	}

	keyID := domain.NewPrefixedID("key")
	created, err := s.appKeys.Create(ctx, domain.AppKey{
		ID:               keyID,
		UserID:           account.UserID,
		ChannelAccountID: channelAccountID,
		AppKeyHash:       hash,
		AppKeyPrefix:     prefix,
		Status:           "active",
	})
	if err != nil {
		return AppKeyResult{}, err
	}

	return AppKeyResult{
		KeyID:        created.ID,
		AppKey:       plaintext,
		AppKeyPrefix: prefix,
		CreatedAt:    created.CreatedAt,
	}, nil
}

type DisableAppKeyInput struct {
	ChannelAccountID string
	KeyID            string
}

func (s *AppKeyService) Disable(ctx context.Context, input DisableAppKeyInput) error {
	if input.ChannelAccountID != "" && input.KeyID != "" {
		return domain.ErrInvalidArg
	}
	if input.KeyID != "" {
		return s.appKeys.DisableByID(ctx, input.KeyID)
	}
	if input.ChannelAccountID != "" {
		return s.appKeys.DisableByChannelAccountID(ctx, input.ChannelAccountID)
	}
	return domain.ErrInvalidArg
}
