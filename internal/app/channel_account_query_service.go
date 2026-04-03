package app

import (
	"context"
	"time"

	"github.com/benenen/channel-plugin/internal/domain"
)

type ChannelAccountQueryService struct {
	users    domain.UserRepository
	accounts domain.ChannelAccountRepository
	appKeys  domain.AppKeyRepository
}

func NewChannelAccountQueryService(
	users domain.UserRepository,
	accounts domain.ChannelAccountRepository,
	appKeys domain.AppKeyRepository,
) *ChannelAccountQueryService {
	return &ChannelAccountQueryService{
		users:    users,
		accounts: accounts,
		appKeys:  appKeys,
	}
}

type ChannelAccountListItem struct {
	ID              string
	ChannelType     string
	AccountUID      string
	DisplayName     string
	AvatarURL       string
	HasActiveAppKey bool
	LastBoundAt     *time.Time
	CreatedAt       time.Time
}

func (s *ChannelAccountQueryService) ListByExternalUserID(ctx context.Context, externalUserID string, channelType string) ([]ChannelAccountListItem, error) {
	user, err := s.users.FindOrCreateByExternalUserID(ctx, externalUserID)
	if err != nil {
		return nil, err
	}

	accounts, err := s.accounts.ListByUserID(ctx, user.ID, channelType)
	if err != nil {
		return nil, err
	}

	result := make([]ChannelAccountListItem, len(accounts))
	for i, acct := range accounts {
		hasKey, _ := s.appKeys.HasActiveByChannelAccountID(ctx, acct.ID)
		result[i] = ChannelAccountListItem{
			ID:              acct.ID,
			ChannelType:     acct.ChannelType,
			AccountUID:      acct.AccountUID,
			DisplayName:     acct.DisplayName,
			AvatarURL:       acct.AvatarURL,
			HasActiveAppKey: hasKey,
			LastBoundAt:     acct.LastBoundAt,
			CreatedAt:       acct.CreatedAt,
		}
	}
	return result, nil
}
