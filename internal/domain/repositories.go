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
	DeleteByBotID(ctx context.Context, botID string) error
}

type BotRepository interface {
	Create(ctx context.Context, bot Bot) (Bot, error)
	GetByID(ctx context.Context, id string) (Bot, error)
	ListByUserID(ctx context.Context, userID string) ([]Bot, error)
	Update(ctx context.Context, bot Bot) (Bot, error)
	DeleteByID(ctx context.Context, id string) error
}
