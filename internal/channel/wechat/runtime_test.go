package wechat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/channel"
)

type testClient struct{}

func (c *testClient) CreateBindingSession(context.Context, string) (CreateSessionResult, error) {
	return CreateSessionResult{}, nil
}

func (c *testClient) GetBindingSession(context.Context, string) (GetSessionResult, error) {
	return GetSessionResult{}, nil
}

func (c *testClient) GetMessages(context.Context, string) ([]Message, error) {
	return []Message{
		{MsgID: "msg_test_1", From: "wxid_1", Text: "hello from test"},
	}, nil
}

func TestStartRuntimeEmitsConnectedAndMessageEvent(t *testing.T) {
	provider := NewProvider(&testClient{})
	connected := false
	messageText := ""
	messageCh := make(chan struct{})

	payload, _ := json.Marshal(map[string]any{
		"openid":   "wxid_1",
		"nickname": "bot-user",
	})

	handle, err := provider.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot_1",
		ChannelType:       "wechat",
		AccountUID:        "wxid_1",
		CredentialPayload: payload,
		CredentialVersion: 1,
		Callbacks: channel.RuntimeCallbacks{
			OnState: func(ev channel.RuntimeStateEvent) {
				if ev.State == channel.RuntimeStateConnected {
					connected = true
				}
			},
			OnEvent: func(ev channel.RuntimeEvent) {
				messageText = ev.Text
				select {
				case messageCh <- struct{}{}:
				default:
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	if !connected {
		t.Fatal("expected connected state")
	}

	select {
	case <-messageCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected inbound message event")
	}

	if messageText == "" {
		t.Fatal("expected inbound message event")
	}
}
