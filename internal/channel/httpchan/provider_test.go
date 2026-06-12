package httpchan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

func TestProviderCreateBinding(t *testing.T) {
	p := NewProvider(nil)
	result, err := p.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_1",
		ChannelType: ChannelType,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderBindingRef != "bind_1" {
		t.Fatalf("expected ProviderBindingRef 'bind_1', got %q", result.ProviderBindingRef)
	}
	if result.ExpiresAt.IsZero() {
		t.Fatal("expected non-zero expires_at")
	}
}

func TestProviderRefreshBinding(t *testing.T) {
	p := NewProvider(nil)
	result, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ProviderBindingRef: "bind_1",
		ChannelType:        ChannelType,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderStatus != "confirmed" {
		t.Fatalf("expected 'confirmed', got %q", result.ProviderStatus)
	}
	if result.AccountUID == "" {
		t.Fatal("expected non-empty account_uid")
	}
	if result.DisplayName == "" {
		t.Fatal("expected non-empty display_name")
	}
	if len(result.CredentialPayload) == 0 {
		t.Fatal("expected non-empty credential_payload")
	}
}

func TestProviderBuildRuntimeConfig(t *testing.T) {
	p := NewProvider(nil)
	cfg, err := p.BuildRuntimeConfig(context.Background(), channel.BuildRuntimeConfigRequest{
		AccountUID:        "http-bind_1",
		ChannelType:       ChannelType,
		CredentialPayload: []byte(`{}`),
		CredentialVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg["credential_blob"] == nil {
		t.Fatal("expected credential_blob in runtime config")
	}
}

func TestReceiverReceive(t *testing.T) {
	recv := NewReceiver()

	var receivedEvent channel.RuntimeEvent
	callbacks := channel.RuntimeCallbacks{
		OnEvent: func(ev channel.RuntimeEvent) {
			receivedEvent = ev
		},
	}

	recv.Register("bot_1", callbacks)
	if !recv.Active("bot_1") {
		t.Fatal("expected bot_1 to be active")
	}

	err := recv.Receive("bot_1", IncomingMessage{
		UserID:      "user_1",
		Text:        "hello",
		MessageID:   "msg_1",
		CallbackURL: "https://example.com/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	if receivedEvent.BotID != "bot_1" {
		t.Fatalf("expected bot_1, got %q", receivedEvent.BotID)
	}
	if receivedEvent.From != "user_1" {
		t.Fatalf("expected user_1, got %q", receivedEvent.From)
	}
	if receivedEvent.Text != "hello" {
		t.Fatalf("expected hello, got %q", receivedEvent.Text)
	}
	if receivedEvent.MessageID != "msg_1" {
		t.Fatalf("expected msg_1, got %q", receivedEvent.MessageID)
	}
	if receivedEvent.ChannelType != ChannelType {
		t.Fatalf("expected http, got %q", receivedEvent.ChannelType)
	}
	if receivedEvent.ReplyTarget.ChannelType != ChannelType {
		t.Fatalf("expected http reply target channel type, got %q", receivedEvent.ReplyTarget.ChannelType)
	}
	if receivedEvent.ReplyTarget.RecipientID != "user_1" {
		t.Fatalf("expected user_1 recipient, got %q", receivedEvent.ReplyTarget.RecipientID)
	}
	cb := receivedEvent.ReplyTarget.MetadataValue("callback_url")
	if cb != "https://example.com/callback" {
		t.Fatalf("expected callback_url, got %q", cb)
	}
}

func TestReceiverReceiveInactive(t *testing.T) {
	recv := NewReceiver()
	err := recv.Receive("bot_not_found", IncomingMessage{
		UserID:      "user_1",
		Text:        "hello",
		CallbackURL: "https://example.com/callback",
	})
	if err == nil {
		t.Fatal("expected error for inactive bot")
	}
}

func TestReceiverUnregister(t *testing.T) {
	recv := NewReceiver()
	recv.Register("bot_1", channel.RuntimeCallbacks{})
	if !recv.Active("bot_1") {
		t.Fatal("expected bot_1 to be active")
	}
	recv.Unregister("bot_1")
	if recv.Active("bot_1") {
		t.Fatal("expected bot_1 to be inactive after unregister")
	}
}

func TestReplyGatewayReply(t *testing.T) {
	var receivedPayload replyPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedPayload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	gw := NewReplyGateway()
	target := channel.ReplyTarget{
		ChannelType: ChannelType,
		RecipientID: "user_1",
		Metadata: map[string]string{
			"callback_url": ts.URL,
		},
	}

	// This uses agent.Response from the agent package. Let's verify with manual HTTP instead.
	// For now, we test that the ReplyGateway sends the POST correctly.
	err := gw.Reply(context.Background(), target, agent.Response{Text: "hello world"})
	if err != nil {
		t.Fatal(err)
	}
	if receivedPayload.Text != "hello world" {
		t.Fatalf("expected 'hello world', got %q", receivedPayload.Text)
	}
	if receivedPayload.UserID != "user_1" {
		t.Fatalf("expected 'user_1', got %q", receivedPayload.UserID)
	}
}

func TestReplyGatewayEmptyText(t *testing.T) {
	gw := NewReplyGateway()
	target := channel.ReplyTarget{
		ChannelType: ChannelType,
		Metadata: map[string]string{
			"callback_url": "https://example.com/callback",
		},
	}
	err := gw.Reply(context.Background(), target, agent.Response{Text: "  "})
	if err != nil {
		t.Fatal("expected no error for empty text")
	}
}

func TestReplyGatewayMissingCallbackURL(t *testing.T) {
	gw := NewReplyGateway()
	target := channel.ReplyTarget{
		ChannelType: ChannelType,
		Metadata:    map[string]string{},
	}
	err := gw.Reply(context.Background(), target, agent.Response{Text: "hello"})
	if err == nil {
		t.Fatal("expected error for missing callback_url")
	}
}

func TestProviderStartRuntimeEmitsConnected(t *testing.T) {
	recv := NewReceiver()
	p := NewProvider(recv)

	connected := false
	handle, err := p.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:       "bot_1",
		ChannelType: ChannelType,
		Callbacks: channel.RuntimeCallbacks{
			OnState: func(ev channel.RuntimeStateEvent) {
				if ev.State == channel.RuntimeStateConnected {
					connected = true
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	if !connected {
		t.Fatal("expected RuntimeStateConnected event")
	}
	if !recv.Active("bot_1") {
		t.Fatal("expected bot_1 to be active in receiver")
	}
}

func TestProviderStartRuntimeStopUnregisters(t *testing.T) {
	recv := NewReceiver()
	p := NewProvider(recv)

	handle, err := p.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:       "bot_1",
		ChannelType: ChannelType,
		Callbacks:   channel.RuntimeCallbacks{},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !recv.Active("bot_1") {
		t.Fatal("expected bot_1 to be active")
	}

	handle.Stop()

	// Wait for the runtime to fully stop (including cleanup)
	<-handle.Done()

	if recv.Active("bot_1") {
		t.Fatal("expected bot_1 to be inactive after stop")
	}
}
