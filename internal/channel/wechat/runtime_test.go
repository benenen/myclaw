package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (c *testClient) GetMessagesLongPoll(context.Context, GetUpdatesOptions) (GetUpdatesResult, error) {
	return GetUpdatesResult{
		Cursor: "cursor_next",
		Messages: []Message{
			{MsgID: "msg_test_1", From: "wxid_1", Text: "hello from test"},
		},
	}, nil
}

type noMessageClient struct{}

func (c *noMessageClient) CreateBindingSession(context.Context, string) (CreateSessionResult, error) {
	return CreateSessionResult{}, nil
}

func (c *noMessageClient) GetBindingSession(context.Context, string) (GetSessionResult, error) {
	return GetSessionResult{}, nil
}

func (c *noMessageClient) GetMessagesLongPoll(context.Context, GetUpdatesOptions) (GetUpdatesResult, error) {
	return GetUpdatesResult{Cursor: "cursor_empty"}, nil
}

type sessionExpiredClient struct{}

func (c *sessionExpiredClient) CreateBindingSession(context.Context, string) (CreateSessionResult, error) {
	return CreateSessionResult{}, nil
}

func (c *sessionExpiredClient) GetBindingSession(context.Context, string) (GetSessionResult, error) {
	return GetSessionResult{}, nil
}

func (c *sessionExpiredClient) GetMessagesLongPoll(context.Context, GetUpdatesOptions) (GetUpdatesResult, error) {
	return GetUpdatesResult{ErrCode: -14, ErrMsg: "session timeout"}, fmt.Errorf("getupdates failed: ret=0 errcode=-14 errmsg=session timeout")
}

func TestStartRuntimeEmitsConnectedAndMessageEvent(t *testing.T) {
	provider := NewProvider(&testClient{}, nil)
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

func TestStartRuntimeEmitsErrorOnSessionExpired(t *testing.T) {
	provider := NewProvider(&sessionExpiredClient{}, nil)
	stateCh := make(chan channel.RuntimeStateEvent, 2)

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
				select {
				case stateCh <- ev:
				default:
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	var gotErr error
	deadline := time.After(200 * time.Millisecond)
	for gotErr == nil {
		select {
		case ev := <-stateCh:
			if ev.State == channel.RuntimeStateError {
				gotErr = ev.Err
			}
		case <-deadline:
			t.Fatal("expected session-expired runtime error")
		}
	}

	if !errors.Is(gotErr, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", gotErr)
	}
}
