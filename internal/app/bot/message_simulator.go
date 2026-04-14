package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/security"
)

type inboundMessageHandler interface {
	HandleMessage(ctx context.Context, msg InboundMessage)
}

type MessageSimulator struct {
	bots         domain.BotRepository
	accounts     domain.ChannelAccountRepository
	cipher       *security.Cipher
	orchestrator inboundMessageHandler
}

type SimulateMessageInput struct {
	BotID       string
	From        string
	Text        string
	MessageID   string
	RecipientID string
}

type SimulateMessageOutput struct {
	BotID       string `json:"bot_id"`
	From        string `json:"from"`
	Text        string `json:"text"`
	MessageID   string `json:"message_id"`
	RecipientID string `json:"recipient_id"`
}

func NewMessageSimulator(
	bots domain.BotRepository,
	accounts domain.ChannelAccountRepository,
	cipher *security.Cipher,
	orchestrator inboundMessageHandler,
) *MessageSimulator {
	return &MessageSimulator{
		bots:         bots,
		accounts:     accounts,
		cipher:       cipher,
		orchestrator: orchestrator,
	}
}

func (s *MessageSimulator) Simulate(ctx context.Context, input SimulateMessageInput) (SimulateMessageOutput, error) {
	botID := strings.TrimSpace(input.BotID)
	from := strings.TrimSpace(input.From)
	messageID := strings.TrimSpace(input.MessageID)
	recipientID := strings.TrimSpace(input.RecipientID)
	if botID == "" || from == "" || strings.TrimSpace(input.Text) == "" {
		return SimulateMessageOutput{}, fmt.Errorf("bot_id, from and text are required: %w", domain.ErrInvalidArg)
	}
	if s == nil || s.orchestrator == nil {
		return SimulateMessageOutput{}, errors.New("message simulator orchestrator is required")
	}
	if messageID == "" {
		messageID = domain.NewPrefixedID("msg")
	}
	if recipientID == "" {
		recipientID = from
	}

	bot, err := s.bots.GetByID(ctx, botID)
	if err != nil {
		return SimulateMessageOutput{}, err
	}
	if bot.ChannelAccountID == "" {
		return SimulateMessageOutput{}, fmt.Errorf("bot channel account is missing: %w", domain.ErrInvalidArg)
	}

	account, err := s.accounts.GetByID(ctx, bot.ChannelAccountID)
	if err != nil {
		return SimulateMessageOutput{}, err
	}
	credentialPayload := account.CredentialCiphertext
	if s.cipher != nil {
		credentialPayload, err = s.cipher.Decrypt(account.CredentialCiphertext)
		if err != nil {
			return SimulateMessageOutput{}, err
		}
	}

	replyTarget, err := s.buildReplyTarget(bot.ChannelType, recipientID, account.AccountUID, credentialPayload)
	if err != nil {
		return SimulateMessageOutput{}, err
	}

	s.orchestrator.HandleMessage(context.Background(), InboundMessage{
		BotID:       botID,
		MessageID:   messageID,
		From:        from,
		Text:        input.Text,
		ReplyTarget: replyTarget,
	})

	return SimulateMessageOutput{
		BotID:       botID,
		From:        from,
		Text:        input.Text,
		MessageID:   messageID,
		RecipientID: recipientID,
	}, nil
}

func (s *MessageSimulator) buildReplyTarget(channelType string, recipientID string, accountUID string, credentialPayload []byte) (channel.ReplyTarget, error) {
	switch channelType {
	case "wechat":
		return wechat.BuildReplyTarget(recipientID, accountUID, credentialPayload)
	default:
		return channel.ReplyTarget{}, fmt.Errorf("unsupported channel type %q: %w", channelType, domain.ErrInvalidArg)
	}
}
