package app

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/security"
)

type BotService struct {
	users    domain.UserRepository
	bots     domain.BotRepository
	bindings domain.ChannelBindingRepository
	accounts domain.ChannelAccountRepository
	cipher   *security.Cipher
	provider channel.Provider
}

func NewBotService(
	users domain.UserRepository,
	bots domain.BotRepository,
	bindings domain.ChannelBindingRepository,
	accounts domain.ChannelAccountRepository,
	cipher *security.Cipher,
	provider channel.Provider,
) *BotService {
	return &BotService{
		users:    users,
		bots:     bots,
		bindings: bindings,
		accounts: accounts,
		cipher:   cipher,
		provider: provider,
	}
}

type CreateBotInput struct {
	ExternalUserID string
	Name           string
	ChannelType    string
}

type CreateBotOutput struct {
	BotID             string
	Name              string
	ChannelType       string
	ConnectionStatus  string
	ChannelAccountID  string
}

func (s *BotService) CreateBot(ctx context.Context, input CreateBotInput) (CreateBotOutput, error) {
	if input.ExternalUserID == "" || input.Name == "" || input.ChannelType == "" {
		return CreateBotOutput{}, domain.ErrInvalidArg
	}
	user, err := s.users.FindOrCreateByExternalUserID(ctx, input.ExternalUserID)
	if err != nil {
		return CreateBotOutput{}, err
	}
	bot, err := s.bots.Create(ctx, domain.Bot{
		ID:               domain.NewPrefixedID("bot"),
		UserID:           user.ID,
		Name:             input.Name,
		ChannelType:      input.ChannelType,
		ConnectionStatus: domain.BotConnectionStatusLoginRequired,
	})
	if err != nil {
		return CreateBotOutput{}, err
	}
	return CreateBotOutput{
		BotID:            bot.ID,
		Name:             bot.Name,
		ChannelType:      bot.ChannelType,
		ConnectionStatus: bot.ConnectionStatus,
		ChannelAccountID: bot.ChannelAccountID,
	}, nil
}

type StartBotLoginInput struct {
	BotID string
}

func (s *BotService) DeleteBot(ctx context.Context, botID string) error {
	if botID == "" {
		return domain.ErrInvalidArg
	}
	if _, err := s.bots.GetByID(ctx, botID); err != nil {
		return err
	}
	if err := s.bindings.DeleteByBotID(ctx, botID); err != nil {
		return err
	}
	return s.bots.DeleteByID(ctx, botID)
}

type BotListItem struct {
	BotID            string
	Name             string
	ChannelType      string
	ConnectionStatus string
	ChannelAccountID string
}

type StartBotLoginOutput struct {
	BotID         string
	BindingID     string
	Status        string
	QRCodePayload string
	QRShareURL    string
	ExpiresAt     *time.Time
}

type RefreshBotLoginOutput struct {
	BotID            string
	BindingID        string
	Status           string
	QRCodePayload    string
	QRShareURL       string
	ExpiresAt        *time.Time
	ChannelAccountID string
	ConnectionStatus string
}

func (s *BotService) RefreshLogin(ctx context.Context, bindingID string) (RefreshBotLoginOutput, error) {
	if bindingID == "" {
		return RefreshBotLoginOutput{}, domain.ErrInvalidArg
	}
	binding, err := s.bindings.GetByID(ctx, bindingID)
	if err != nil {
		return RefreshBotLoginOutput{}, err
	}
	bot, err := s.bots.GetByID(ctx, binding.BotID)
	if err != nil {
		return RefreshBotLoginOutput{}, err
	}
	result, err := s.provider.RefreshBinding(ctx, channel.RefreshBindingRequest{ProviderBindingRef: binding.ProviderBindingRef})
	if err != nil {
		return RefreshBotLoginOutput{}, err
	}
	binding.Status = result.ProviderStatus
	binding.QRCodePayload = result.QRCodePayload
	if !result.ExpiresAt.IsZero() {
		binding.ExpiresAt = &result.ExpiresAt
	}
	binding.ErrorMessage = result.ErrorMessage
	if result.ProviderStatus == domain.BindingStatusConfirmed {
		now := time.Now().UTC()
		ciphertext, err := s.cipher.Encrypt(result.CredentialPayload)
		if err != nil {
			return RefreshBotLoginOutput{}, err
		}
		account, err := s.accounts.Upsert(ctx, domain.ChannelAccount{
			ID:                   domain.NewPrefixedID("acct"),
			UserID:               bot.UserID,
			ChannelType:          bot.ChannelType,
			AccountUID:           result.AccountUID,
			DisplayName:          result.DisplayName,
			AvatarURL:            result.AvatarURL,
			CredentialCiphertext: ciphertext,
			CredentialVersion:    result.CredentialVersion,
			LastBoundAt:          &now,
		})
		if err != nil {
			return RefreshBotLoginOutput{}, err
		}
		binding.ChannelAccountID = account.ID
		binding.FinishedAt = &now
		bot.ChannelAccountID = account.ID
		bot.ConnectionStatus = domain.BotConnectionStatusConnected
		bot.ConnectionError = ""
		bot.LastConnectedAt = &now
		if _, err := s.bots.Update(ctx, bot); err != nil {
			return RefreshBotLoginOutput{}, err
		}
	} else if result.ProviderStatus == domain.BindingStatusFailed || result.ProviderStatus == domain.BindingStatusExpired {
		now := time.Now().UTC()
		binding.FinishedAt = &now
		bot.ConnectionStatus = domain.BotConnectionStatusError
		bot.ConnectionError = result.ErrorMessage
		if bot.ConnectionError == "" {
			bot.ConnectionError = result.ProviderStatus
		}
		if _, err := s.bots.Update(ctx, bot); err != nil {
			return RefreshBotLoginOutput{}, err
		}
	}
	binding, err = s.bindings.Update(ctx, binding)
	if err != nil {
		return RefreshBotLoginOutput{}, err
	}
	bot, err = s.bots.GetByID(ctx, bot.ID)
	if err != nil {
		return RefreshBotLoginOutput{}, err
	}
	return RefreshBotLoginOutput{
		BotID:            bot.ID,
		BindingID:        binding.ID,
		Status:           binding.Status,
		QRCodePayload:    binding.QRCodePayload,
		QRShareURL:       binding.QRCodePayload,
		ExpiresAt:        binding.ExpiresAt,
		ChannelAccountID: binding.ChannelAccountID,
		ConnectionStatus: bot.ConnectionStatus,
	}, nil
}

func (s *BotService) ListBots(ctx context.Context, externalUserID string) ([]BotListItem, error) {
	if externalUserID == "" {
		return nil, domain.ErrInvalidArg
	}
	user, err := s.users.FindOrCreateByExternalUserID(ctx, externalUserID)
	if err != nil {
		return nil, err
	}
	bots, err := s.bots.ListByUserID(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	items := make([]BotListItem, 0, len(bots))
	for _, bot := range bots {
		items = append(items, BotListItem{
			BotID:            bot.ID,
			Name:             bot.Name,
			ChannelType:      bot.ChannelType,
			ConnectionStatus: bot.ConnectionStatus,
			ChannelAccountID: bot.ChannelAccountID,
		})
	}
	return items, nil
}

func (s *BotService) StartLogin(ctx context.Context, input StartBotLoginInput) (StartBotLoginOutput, error) {
	if input.BotID == "" {
		return StartBotLoginOutput{}, domain.ErrInvalidArg
	}
	bot, err := s.bots.GetByID(ctx, input.BotID)
	if err != nil {
		return StartBotLoginOutput{}, err
	}
	bindingID := domain.NewPrefixedID("bind")
	binding, err := s.bindings.Create(ctx, domain.ChannelBinding{
		ID:          bindingID,
		BotID:       bot.ID,
		UserID:      bot.UserID,
		ChannelType: bot.ChannelType,
		Status:      domain.BindingStatusPending,
	})
	if err != nil {
		return StartBotLoginOutput{}, err
	}
	result, err := s.provider.CreateBinding(ctx, channel.CreateBindingRequest{
		BindingID:   bindingID,
		ChannelType: bot.ChannelType,
	})
	if err != nil {
		binding.Status = domain.BindingStatusFailed
		binding.ErrorMessage = err.Error()
		now := time.Now().UTC()
		binding.FinishedAt = &now
		_, _ = s.bindings.Update(ctx, binding)
		return StartBotLoginOutput{}, err
	}
	binding.Status = domain.BindingStatusQRReady
	binding.ProviderBindingRef = result.ProviderBindingRef
	binding.QRCodePayload = result.QRCodePayload
	binding.ExpiresAt = &result.ExpiresAt
	binding, err = s.bindings.Update(ctx, binding)
	if err != nil {
		return StartBotLoginOutput{}, err
	}
	return StartBotLoginOutput{
		BotID:         bot.ID,
		BindingID:     binding.ID,
		Status:        binding.Status,
		QRCodePayload: binding.QRCodePayload,
		QRShareURL:    result.QRShareURL,
		ExpiresAt:     binding.ExpiresAt,
	}, nil
}
