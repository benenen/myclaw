package domain

import "context"

type UserRepository interface {
	FindOrCreateByExternalUserID(ctx context.Context, externalUserID string) (User, error)
}

type ChannelAccountRepository interface {
	Upsert(ctx context.Context, account ChannelAccount) (ChannelAccount, error)
	GetByID(ctx context.Context, id string) (ChannelAccount, error)
	ListByUserID(ctx context.Context, userID string, channelType string) ([]ChannelAccount, error)
}

type ChannelBindingRepository interface {
	Create(ctx context.Context, binding ChannelBinding) (ChannelBinding, error)
	GetByID(ctx context.Context, id string) (ChannelBinding, error)
	Update(ctx context.Context, binding ChannelBinding) (ChannelBinding, error)
}

type AppKeyRepository interface {
	Create(ctx context.Context, key AppKey) (AppKey, error)
	FindByHash(ctx context.Context, hash string) (AppKey, error)
	FindActiveByChannelAccountID(ctx context.Context, channelAccountID string) (AppKey, error)
	DisableByID(ctx context.Context, id string) error
	DisableByChannelAccountID(ctx context.Context, channelAccountID string) error
	UpdateLastUsedAt(ctx context.Context, id string) error
	HasActiveByChannelAccountID(ctx context.Context, channelAccountID string) (bool, error)
}
