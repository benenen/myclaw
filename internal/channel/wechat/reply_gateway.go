package wechat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type messageSender interface {
	SendTextMessage(ctx context.Context, opts SendMessageOptions) error
}

var ErrMissingReplyRecipient = errors.New("wechat reply target recipient_id is required")
var ErrMissingReplyBaseURL = errors.New("wechat reply target metadata base_url is required")
var ErrMissingReplyToken = errors.New("wechat reply target metadata token is required")
var ErrMissingReplyWechatUIN = errors.New("wechat reply target metadata wechat_uin is required")

type ReplyGateway struct {
	client messageSender
}

func validateReplyTarget(target channel.ReplyTarget) error {
	if strings.TrimSpace(target.RecipientID) == "" {
		return ErrMissingReplyRecipient
	}
	if strings.TrimSpace(target.MetadataValue("base_url")) == "" {
		return ErrMissingReplyBaseURL
	}
	if strings.TrimSpace(target.MetadataValue("token")) == "" {
		return ErrMissingReplyToken
	}
	if strings.TrimSpace(target.MetadataValue("wechat_uin")) == "" {
		return ErrMissingReplyWechatUIN
	}
	return nil
}

func trimmedMetadataValue(target channel.ReplyTarget, key string) string {
	return strings.TrimSpace(target.MetadataValue(key))
}

func trimmedRecipientID(target channel.ReplyTarget) string {
	return strings.TrimSpace(target.RecipientID)
}

func NewReplyGateway(client messageSender) *ReplyGateway {
	return &ReplyGateway{client: client}
}

func (g *ReplyGateway) Reply(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error {
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil
	}
	if err := validateReplyTarget(target); err != nil {
		return err
	}
	if err := g.client.SendTextMessage(ctx, SendMessageOptions{
		BaseURL:   trimmedMetadataValue(target, "base_url"),
		Token:     trimmedMetadataValue(target, "token"),
		WechatUIN: trimmedMetadataValue(target, "wechat_uin"),
		ToUserID:  trimmedRecipientID(target),
		Text:      text,
	}); err != nil {
		return fmt.Errorf("wechat reply: %w", err)
	}
	return nil
}
