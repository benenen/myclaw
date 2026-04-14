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

func (c *testClient) SendTextMessage(context.Context, SendMessageOptions) error {
	return nil
}

type pollingClient struct {
	polls chan GetUpdatesOptions
	resp  GetUpdatesResult
	err   error
}

func (c *pollingClient) CreateBindingSession(context.Context, string) (CreateSessionResult, error) {
	return CreateSessionResult{}, nil
}

func (c *pollingClient) GetBindingSession(context.Context, string) (GetSessionResult, error) {
	return GetSessionResult{}, nil
}

func (c *pollingClient) GetMessagesLongPoll(_ context.Context, opts GetUpdatesOptions) (GetUpdatesResult, error) {
	if c.polls != nil {
		select {
		case c.polls <- opts:
		default:
		}
	}
	return c.resp, c.err
}

func (c *pollingClient) SendTextMessage(context.Context, SendMessageOptions) error {
	return nil
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

func (c *sessionExpiredClient) SendTextMessage(context.Context, SendMessageOptions) error {
	return nil
}

func TestStartRuntimeEmitsConnectedAndMessageEvent(t *testing.T) {
	provider := NewProvider(&testClient{}, nil)
	connected := false
	messageText := ""
	var replyTarget channel.ReplyTarget
	messageCh := make(chan struct{})

	payload, _ := json.Marshal(map[string]any{
		"openid":       "wxid_1",
		"nickname":     "bot-user",
		"baseurl":      "https://wechat.example",
		"bot_token":    "token-1",
		"wechat_uin":   "uin-1",
		"display_name": "bot-user",
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
				replyTarget = ev.ReplyTarget
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
	if replyTarget.ChannelType != "wechat" || replyTarget.RecipientID != "wxid_1" {
		t.Fatalf("unexpected reply target: %#v", replyTarget)
	}
	if replyTarget.MetadataValue("base_url") != "https://wechat.example" || replyTarget.MetadataValue("token") != "token-1" || replyTarget.MetadataValue("wechat_uin") != "uin-1" {
		t.Fatalf("unexpected reply target metadata: %#v", replyTarget.Metadata)
	}
	if replyTarget.MetadataValue("context_token") != "" {
		t.Fatalf("unexpected context token: %#v", replyTarget.Metadata)
	}
}

func TestStartRuntimePropagatesContextTokenToReplyTarget(t *testing.T) {
	provider := NewProvider(&pollingClient{resp: GetUpdatesResult{Messages: []Message{{MsgID: "msg_test_1", From: "wxid_1", Text: "hello from test", ContextToken: "ctx-1"}}}}, nil)
	messageCh := make(chan channel.RuntimeEvent, 1)

	payload, _ := json.Marshal(map[string]any{
		"openid":     "wxid_1",
		"baseurl":    "https://wechat.example",
		"bot_token":  "token-1",
		"wechat_uin": "uin-1",
	})

	handle, err := provider.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot_1",
		ChannelType:       "wechat",
		AccountUID:        "wxid_1",
		CredentialPayload: payload,
		CredentialVersion: 1,
		Callbacks: channel.RuntimeCallbacks{
			OnEvent: func(ev channel.RuntimeEvent) {
				select {
				case messageCh <- ev:
				default:
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	select {
	case ev := <-messageCh:
		if ev.ReplyTarget.MetadataValue("context_token") != "ctx-1" {
			t.Fatalf("unexpected context token: %#v", ev.ReplyTarget.Metadata)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected inbound message event")
	}
}

func TestStartRuntimeOmitsEmptyContextToken(t *testing.T) {
	provider := NewProvider(&pollingClient{resp: GetUpdatesResult{Messages: []Message{{MsgID: "msg_test_1", From: "wxid_1", Text: "hello from test"}}}}, nil)
	messageCh := make(chan channel.RuntimeEvent, 1)

	payload, _ := json.Marshal(map[string]any{
		"openid":     "wxid_1",
		"baseurl":    "https://wechat.example",
		"bot_token":  "token-1",
		"wechat_uin": "uin-1",
	})

	handle, err := provider.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot_1",
		ChannelType:       "wechat",
		AccountUID:        "wxid_1",
		CredentialPayload: payload,
		CredentialVersion: 1,
		Callbacks: channel.RuntimeCallbacks{
			OnEvent: func(ev channel.RuntimeEvent) {
				select {
				case messageCh <- ev:
				default:
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	select {
	case ev := <-messageCh:
		if ev.ReplyTarget.MetadataValue("context_token") != "" {
			t.Fatalf("unexpected context token: %#v", ev.ReplyTarget.Metadata)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected inbound message event")
	}
}

func TestStartRuntimePreservesPollTimeoutWhenServerReturnsZeroNextTimeout(t *testing.T) {
	polls := make(chan GetUpdatesOptions, 3)
	provider := NewProvider(&pollingClient{polls: polls, resp: GetUpdatesResult{Cursor: "cursor_empty"}}, nil)
	payload, _ := json.Marshal(map[string]any{"openid": "wxid_1", "nickname": "bot-user"})

	handle, err := provider.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot_1",
		ChannelType:       "wechat",
		AccountUID:        "wxid_1",
		CredentialPayload: payload,
		CredentialVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	first := <-polls
	second := <-polls
	third := <-polls

	if first.Timeout != 35*time.Second || second.Timeout != 35*time.Second || third.Timeout != 35*time.Second {
		t.Fatalf("unexpected poll timeouts: %s, %s, %s", first.Timeout, second.Timeout, third.Timeout)
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

func TestClassifyRuntimePollErrorDetectsSessionExpiryWithoutStructuredErrCode(t *testing.T) {
	err := classifyRuntimePollError(errors.New("getupdates failed: ret=0 errcode=-14 errmsg=session timeout"))
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

func TestClassifyRuntimePollErrorLeavesOtherErrorsUntouched(t *testing.T) {
	original := errors.New("temporary network failure")
	if got := classifyRuntimePollError(original); !errors.Is(got, original) {
		t.Fatalf("expected original error, got %v", got)
	}
}

func TestNextPollTimeoutKeepsPreviousTimeoutOnZero(t *testing.T) {
	if got := nextPollTimeout(12*time.Second, 0); got != 12*time.Second {
		t.Fatalf("expected previous timeout, got %s", got)
	}
}

func TestNextPollTimeoutFallsBackToDefault(t *testing.T) {
	if got := nextPollTimeout(0, 0); got != 35*time.Second {
		t.Fatalf("expected default timeout, got %s", got)
	}
}

func TestNextPollTimeoutUsesServerValueWhenPositive(t *testing.T) {
	if got := nextPollTimeout(12*time.Second, 5*time.Second); got != 5*time.Second {
		t.Fatalf("expected server timeout, got %s", got)
	}
}

func TestClassifyRuntimePollErrorDetectsSessionExpiredMessage(t *testing.T) {
	err := classifyRuntimePollError(errors.New("session expired while polling"))
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

func TestStartRuntimeEmitsErrorOnSessionExpiredWithoutErrCodeResult(t *testing.T) {
	provider := NewProvider(&pollingClient{err: errors.New("getupdates failed: session timeout")}, nil)
	stateCh := make(chan channel.RuntimeStateEvent, 2)
	payload, _ := json.Marshal(map[string]any{"openid": "wxid_1", "nickname": "bot-user"})

	handle, err := provider.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot_1",
		ChannelType:       "wechat",
		AccountUID:        "wxid_1",
		CredentialPayload: payload,
		CredentialVersion: 1,
		Callbacks: channel.RuntimeCallbacks{OnState: func(ev channel.RuntimeStateEvent) {
			select {
			case stateCh <- ev:
			default:
			}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	select {
	case ev := <-stateCh:
		if ev.State == channel.RuntimeStateConnected {
			ev = <-stateCh
		}
		if ev.State != channel.RuntimeStateError || !errors.Is(ev.Err, ErrSessionExpired) {
			t.Fatalf("unexpected state event: %#v", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected session-expired runtime error")
	}
}
