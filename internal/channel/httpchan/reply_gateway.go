package httpchan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type ReplyGateway struct {
	client   *http.Client
	receiver *Receiver
}

func NewReplyGateway() *ReplyGateway {
	return &ReplyGateway{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// NewReplyGatewayWithReceiver creates a ReplyGateway that also delivers
// replies to in-process chat waiters (for the built-in web console chat).
func NewReplyGatewayWithReceiver(receiver *Receiver) *ReplyGateway {
	return &ReplyGateway{
		client:   &http.Client{Timeout: 10 * time.Second},
		receiver: receiver,
	}
}

type replyPayload struct {
	BotID     string `json:"bot_id"`
	UserID    string `json:"user_id"`
	Text      string `json:"text"`
	MessageID string `json:"message_id,omitempty"`
}

func (g *ReplyGateway) Reply(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error {
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil
	}

	// Try to deliver to an in-process chat waiter first.
	botID := target.MetadataValue("bot_id")
	messageID := target.MetadataValue("message_id")
	if g.receiver != nil && botID != "" && messageID != "" {
		if g.receiver.DeliverChatReply(botID, messageID, resp) {
			return nil
		}
	}

	callbackURL := strings.TrimSpace(target.MetadataValue("callback_url"))
	if callbackURL == "" {
		return fmt.Errorf("httpchan: callback_url is required in reply target metadata")
	}

	payload := replyPayload{
		UserID: strings.TrimSpace(target.RecipientID),
		Text:   text,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("httpchan: marshal reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("httpchan: create reply request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("httpchan: reply request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("httpchan: reply got status %d", res.StatusCode)
	}

	return nil
}
