package feishu

import (
	"context"
	"fmt"
	"strings"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type ReplyGateway struct {
	api      feishuAPI
	registry *Registry
}

func NewReplyGateway(api feishuAPI, registry *Registry) *ReplyGateway {
	return &ReplyGateway{api: api, registry: registry}
}

func (g *ReplyGateway) Reply(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error {
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil
	}
	botID := strings.TrimSpace(target.MetadataValue("bot_id"))
	if botID == "" {
		return fmt.Errorf("feishu reply: bot_id metadata required")
	}
	creds, ok := g.registry.Lookup(botID)
	if !ok {
		return fmt.Errorf("feishu reply: bot %q is not active", botID)
	}
	chatID := strings.TrimSpace(target.MetadataValue("chat_id"))
	if chatID == "" {
		chatID = strings.TrimSpace(target.RecipientID)
	}
	params := SendParams{ChatID: chatID, Text: text}
	if target.MetadataValue("chat_type") == "group" {
		params.ReplyMessageID = strings.TrimSpace(target.MetadataValue("message_id"))
		if senderOpenID := strings.TrimSpace(target.MetadataValue("sender_open_id")); senderOpenID != "" {
			params.Mentions = []string{senderOpenID}
		}
	}
	if err := g.api.SendText(ctx, creds, params); err != nil {
		return fmt.Errorf("feishu reply: %w", err)
	}
	return nil
}

var _ channel.ReplyGateway = (*ReplyGateway)(nil)
