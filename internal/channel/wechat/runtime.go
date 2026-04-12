package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/benenen/myclaw/internal/channel"
)

var ErrSessionExpired = errors.New("wechat session expired")

type wechatRuntimeHandle struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func (h *wechatRuntimeHandle) Stop() {
	h.cancel()
}

func (h *wechatRuntimeHandle) Done() <-chan struct{} {
	return h.done
}

func (p *Provider) StartRuntime(ctx context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	runtimeCtx, cancel := context.WithCancel(ctx)
	handle := &wechatRuntimeHandle{done: make(chan struct{}), cancel: cancel}

	var payload map[string]any
	if err := json.Unmarshal(req.CredentialPayload, &payload); err != nil {
		cancel()
		return nil, fmt.Errorf("unmarshal runtime credential payload: %w", err)
	}
	baseURL, _ := payload["baseurl"].(string)
	botToken, _ := payload["bot_token"].(string)
	wechatUIN, _ := payload["wechat_uin"].(string)
	if wechatUIN == "" {
		wechatUIN = randomWechatUIN()
	}
	payload["wechat_uin"] = wechatUIN
	if updatedPayload, err := json.Marshal(payload); err == nil {
		req.CredentialPayload = updatedPayload
	}

	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateConnected,
		})
	}

	go func() {
		defer close(handle.done)

		cursor := ""
		pollTimeout := 35 * time.Second

		for {
			select {
			case <-runtimeCtx.Done():
				if req.Callbacks.OnState != nil {
					req.Callbacks.OnState(channel.RuntimeStateEvent{
						BotID:       req.BotID,
						ChannelType: req.ChannelType,
						State:       channel.RuntimeStateStopped,
						Reason:      runtimeCtx.Err().Error(),
					})
				}
				return
			default:
				updates, err := p.client.GetMessagesLongPoll(runtimeCtx, GetUpdatesOptions{BaseURL: baseURL, Token: botToken, WechatUIN: wechatUIN, Cursor: cursor, Timeout: pollTimeout})
				if err != nil {
					if runtimeCtx.Err() != nil {
						return
					}
					p.logger.Info("wechat runtime poll error", "bot_id", req.BotID, "error", err)
					if updates.ErrCode == -14 {
						if req.Callbacks.OnState != nil {
							req.Callbacks.OnState(channel.RuntimeStateEvent{
								BotID:       req.BotID,
								ChannelType: req.ChannelType,
								State:       channel.RuntimeStateError,
								Err:         fmt.Errorf("%w: %v", ErrSessionExpired, err),
							})
						}
						return
					}
					time.Sleep(2 * time.Second)
					continue
				}
				p.logger.Debug("wechat runtime poll ok", "bot_id", req.BotID, "messages", len(updates.Messages), "cursor_len", len(updates.Cursor), "next_timeout", updates.NextTimeout)
				if updates.Cursor != "" {
					cursor = updates.Cursor
				}
				if updates.NextTimeout > 0 {
					pollTimeout = updates.NextTimeout
				}
				for _, msg := range updates.Messages {
					if req.Callbacks.OnEvent != nil {
						req.Callbacks.OnEvent(channel.RuntimeEvent{
							BotID:       req.BotID,
							ChannelType: req.ChannelType,
							MessageID:   msg.MsgID,
							From:        msg.From,
							Text:        msg.Text,
							Raw:         msg.Raw,
						})
					}
				}
			}
		}
	}()

	_ = payload
	return handle, nil
}

var _ channel.RuntimeStarter = (*Provider)(nil)
