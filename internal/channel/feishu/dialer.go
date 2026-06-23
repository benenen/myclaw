package feishu

import (
	"context"
	"encoding/json"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type wsDialer struct{}

func NewDialer() *wsDialer { return &wsDialer{} }

func (wsDialer) Dial(creds Credentials, onMessage func(InboundMessage)) (conn, error) {
	handler := larkdispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			onMessage(normalizeMessage(event))
			return nil
		}).
		OnP2MessageReadV1(func(_ context.Context, _ *larkim.P2MessageReadV1) error {
			// Read receipts need no handling; register a no-op so the SDK stops
			// logging "not found handler" for every im.message.message_read_v1 event.
			return nil
		}).
		OnP2ChatAccessEventBotP2pChatEnteredV1(func(_ context.Context, _ *larkim.P2ChatAccessEventBotP2pChatEnteredV1) error {
			// "user entered the bot's p2p chat" needs no handling; no-op silences the
			// SDK's "not found handler" error for im.chat.access_event.bot_p2p_chat_entered_v1.
			return nil
		})
	cli := larkws.NewClient(creds.AppID, creds.AppSecret,
		larkws.WithEventHandler(handler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)
	return &wsConn{cli: cli}, nil
}

type wsConn struct {
	cli *larkws.Client
}

// Start runs the SDK's long-connection loop. It blocks until ctx is cancelled
// and reconnects internally on transient drops.
func (c *wsConn) Start(ctx context.Context) error {
	return c.cli.Start(ctx)
}

func normalizeMessage(event *larkim.P2MessageReceiveV1) InboundMessage {
	msg := event.Event.Message
	in := InboundMessage{
		MessageID: derefStr(msg.MessageId),
		ChatID:    derefStr(msg.ChatId),
		ChatType:  derefStr(msg.ChatType),
		Text:      parseTextContent(derefStr(msg.MessageType), derefStr(msg.Content)),
	}
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		in.SenderOpenID = derefStr(event.Event.Sender.SenderId.OpenId)
	}
	for _, m := range msg.Mentions {
		if m != nil && m.Id != nil {
			in.MentionedOpenIDs = append(in.MentionedOpenIDs, derefStr(m.Id.OpenId))
		}
	}
	return in
}

func parseTextContent(msgType, content string) string {
	if msgType != "text" {
		return ""
	}
	var c struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(content), &c)
	return c.Text
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ dialer = (*wsDialer)(nil)
