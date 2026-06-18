package feishu

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benenen/myclaw/internal/channel"
)

type runtimeHandle struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func (h *runtimeHandle) Stop()                 { h.cancel() }
func (h *runtimeHandle) Done() <-chan struct{} { return h.done }

func (p *Provider) StartRuntime(ctx context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	var creds Credentials
	if err := json.Unmarshal(req.CredentialPayload, &creds); err != nil {
		return nil, fmt.Errorf("feishu unmarshal credentials: %w", err)
	}
	if creds.AppID == "" || creds.AppSecret == "" {
		return nil, fmt.Errorf("feishu: runtime credentials missing app_id/app_secret")
	}

	onMessage := func(msg InboundMessage) {
		if !shouldHandle(msg, creds.BotOpenID) {
			return
		}
		if req.Callbacks.OnEvent == nil {
			return
		}
		req.Callbacks.OnEvent(channel.RuntimeEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			MessageID:   msg.MessageID,
			From:        msg.SenderOpenID,
			Text:        msg.Text,
			ReplyTarget: channel.ReplyTarget{
				ChannelType: req.ChannelType,
				RecipientID: msg.ChatID,
				Metadata: map[string]string{
					"bot_id":         req.BotID,
					"chat_id":        msg.ChatID,
					"chat_type":      msg.ChatType,
					"message_id":     msg.MessageID,
					"sender_open_id": msg.SenderOpenID,
				},
			},
		})
	}

	c, err := p.dialer.Dial(creds, onMessage)
	if err != nil {
		return nil, fmt.Errorf("feishu dial: %w", err)
	}

	p.registry.Register(req.BotID, creds)

	runtimeCtx, cancel := context.WithCancel(ctx)
	handle := &runtimeHandle{done: make(chan struct{}), cancel: cancel}

	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateConnected,
		})
	}

	go func() {
		defer cancel()
		defer close(handle.done)
		defer p.registry.Unregister(req.BotID)

		startErr := c.Start(runtimeCtx)
		if req.Callbacks.OnState == nil {
			return
		}
		if runtimeCtx.Err() != nil {
			req.Callbacks.OnState(channel.RuntimeStateEvent{
				BotID:       req.BotID,
				ChannelType: req.ChannelType,
				State:       channel.RuntimeStateStopped,
				Reason:      runtimeCtx.Err().Error(),
			})
			return
		}
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateError,
			Err:         startErr,
		})
	}()

	return handle, nil
}

// shouldHandle decides whether an inbound message should be forwarded:
// p2p always; group only when the bot itself is @mentioned. Empty-text
// messages (non-text payloads) are skipped.
func shouldHandle(msg InboundMessage, botOpenID string) bool {
	if msg.Text == "" {
		return false
	}
	if msg.ChatType == "p2p" {
		return true
	}
	if botOpenID == "" {
		return false
	}
	for _, id := range msg.MentionedOpenIDs {
		if id == botOpenID {
			return true
		}
	}
	return false
}

var _ channel.RuntimeStarter = (*Provider)(nil)
