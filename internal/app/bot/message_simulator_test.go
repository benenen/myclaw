package bot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/security"
)

type channelAccountRepoStub struct {
	account domain.ChannelAccount
}

func (r *channelAccountRepoStub) Upsert(context.Context, domain.ChannelAccount) (domain.ChannelAccount, error) {
	panic("unexpected call")
}

func (r *channelAccountRepoStub) GetByID(_ context.Context, id string) (domain.ChannelAccount, error) {
	if r.account.ID != id {
		return domain.ChannelAccount{}, domain.ErrNotFound
	}
	return r.account, nil
}

func (r *channelAccountRepoStub) ListByUserID(context.Context, string, string) ([]domain.ChannelAccount, error) {
	panic("unexpected call")
}

type captureMessageHandler struct {
	msgCh chan InboundMessage
}

func (h captureMessageHandler) HandleMessage(_ context.Context, msg InboundMessage) {
	h.msgCh <- msg
}

func testCipher(t *testing.T) *security.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, err := security.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestMessageSimulatorSimulateBuildsInboundMessage(t *testing.T) {
	cipher := testCipher(t)
	payload, err := json.Marshal(map[string]any{
		"baseurl":   "https://wechat.example",
		"bot_token": "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}

	msgCh := make(chan InboundMessage, 1)
	simulator := NewMessageSimulator(
		newBotRepoStub(domain.Bot{
			ID:               "bot_1",
			ChannelType:      "wechat",
			ChannelAccountID: "acct_1",
		}),
		&channelAccountRepoStub{account: domain.ChannelAccount{
			ID:                   "acct_1",
			AccountUID:           "wxid_1",
			CredentialCiphertext: ciphertext,
		}},
		cipher,
		captureMessageHandler{msgCh: msgCh},
	)

	_, err = simulator.Simulate(context.Background(), SimulateMessageInput{
		BotID: "bot_1",
		From:  "user_1",
		Text:  "hello",
	})
	if err == nil {
		t.Fatal("expected missing recipient_id error")
	}
	if !errors.Is(err, domain.ErrInvalidArg) || !strings.Contains(err.Error(), "recipient_id and text are required") {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case msg := <-msgCh:
		t.Fatalf("unexpected message: %#v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMessageSimulatorSimulateUsesExplicitRecipientID(t *testing.T) {
	cipher := testCipher(t)
	payload, err := json.Marshal(map[string]any{
		"baseurl":   "https://wechat.example",
		"bot_token": "token-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}

	msgCh := make(chan InboundMessage, 1)
	simulator := NewMessageSimulator(
		newBotRepoStub(domain.Bot{
			ID:               "bot_1",
			ChannelType:      "wechat",
			ChannelAccountID: "acct_1",
		}),
		&channelAccountRepoStub{account: domain.ChannelAccount{
			ID:                   "acct_1",
			AccountUID:           "wxid_1",
			CredentialCiphertext: ciphertext,
		}},
		cipher,
		captureMessageHandler{msgCh: msgCh},
	)

	got, err := simulator.Simulate(context.Background(), SimulateMessageInput{
		BotID:       "bot_1",
		From:        "user_1",
		RecipientID: "wx_real_user_1",
		Text:        "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID == "" {
		t.Fatal("expected generated message id")
	}
	if got.RecipientID != "wx_real_user_1" {
		t.Fatalf("unexpected recipient id: %q", got.RecipientID)
	}

	select {
	case msg := <-msgCh:
		if msg.BotID != "bot_1" {
			t.Fatalf("unexpected bot id: %q", msg.BotID)
		}
		if msg.MessageID != got.MessageID {
			t.Fatalf("unexpected message id: %q", msg.MessageID)
		}
		if msg.From != "user_1" {
			t.Fatalf("unexpected from: %q", msg.From)
		}
		if msg.Text != "hello" {
			t.Fatalf("unexpected text: %q", msg.Text)
		}
		if msg.ReplyTarget.ChannelType != "wechat" {
			t.Fatalf("unexpected channel type: %q", msg.ReplyTarget.ChannelType)
		}
		if msg.ReplyTarget.RecipientID != "wx_real_user_1" {
			t.Fatalf("unexpected reply recipient: %q", msg.ReplyTarget.RecipientID)
		}
		if msg.ReplyTarget.MetadataValue("account_uid") != "wxid_1" {
			t.Fatalf("unexpected account uid: %q", msg.ReplyTarget.MetadataValue("account_uid"))
		}
		if msg.ReplyTarget.MetadataValue("base_url") != "https://wechat.example" {
			t.Fatalf("unexpected base url: %q", msg.ReplyTarget.MetadataValue("base_url"))
		}
		if msg.ReplyTarget.MetadataValue("token") != "token-1" {
			t.Fatalf("unexpected token: %q", msg.ReplyTarget.MetadataValue("token"))
		}
		if msg.ReplyTarget.MetadataValue("wechat_uin") == "" {
			t.Fatal("expected generated wechat_uin")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for simulated message")
	}
}

func TestMessageSimulatorSimulateTriggersReplyGateway(t *testing.T) {
	cipher := testCipher(t)
	payload, err := json.Marshal(map[string]any{
		"baseurl":    "https://wechat.example",
		"bot_token":  "token-1",
		"wechat_uin": "uin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}

	replyCh := make(chan struct {
		target channel.ReplyTarget
		resp   agent.Response
	}, 1)
	orchestrator := NewBotMessageOrchestrator(
		&fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
			if req.MessageID == "" {
				t.Fatal("expected message id to be populated")
			}
			return agent.Response{Text: "pong:" + req.Prompt}, nil
		}},
		fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, resp agent.Response) error {
			replyCh <- struct {
				target channel.ReplyTarget
				resp   agent.Response
			}{target: target, resp: resp}
			return nil
		}},
		fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
			return defaultTestSpec, nil
		}},
	)

	simulator := NewMessageSimulator(
		newBotRepoStub(domain.Bot{
			ID:               "bot_1",
			ChannelType:      "wechat",
			ChannelAccountID: "acct_1",
		}),
		&channelAccountRepoStub{account: domain.ChannelAccount{
			ID:                   "acct_1",
			AccountUID:           "wxid_1",
			CredentialCiphertext: ciphertext,
		}},
		cipher,
		orchestrator,
	)

	if _, err := simulator.Simulate(context.Background(), SimulateMessageInput{
		BotID:       "bot_1",
		From:        "user_1",
		RecipientID: "debug_target",
		Text:        "ping",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-replyCh:
		if got.target.RecipientID != "debug_target" {
			t.Fatalf("unexpected reply recipient: %q", got.target.RecipientID)
		}
		if got.target.MetadataValue("base_url") != "https://wechat.example" {
			t.Fatalf("unexpected base url: %q", got.target.MetadataValue("base_url"))
		}
		if got.target.MetadataValue("token") != "token-1" {
			t.Fatalf("unexpected token: %q", got.target.MetadataValue("token"))
		}
		if got.target.MetadataValue("wechat_uin") != "uin-1" {
			t.Fatalf("unexpected wechat_uin: %q", got.target.MetadataValue("wechat_uin"))
		}
		if got.resp.Text != "pong:ping" {
			t.Fatalf("unexpected reply text: %q", got.resp.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
}
