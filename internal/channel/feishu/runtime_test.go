package feishu

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/channel"
)

func startTestRuntime(t *testing.T) (*Provider, *fakeConn, *Registry, chan channel.RuntimeEvent, channel.RuntimeHandle) {
	t.Helper()
	fc := newFakeConn()
	registry := NewRegistry()
	p := NewProvider(&fakeAPI{}, &fakeDialer{conn: fc}, registry, nil)

	events := make(chan channel.RuntimeEvent, 4)
	payload, _ := json.Marshal(Credentials{AppID: "cli_x", AppSecret: "s", BotOpenID: "ou_bot"})
	handle, err := p.StartRuntime(context.Background(), channel.StartRuntimeRequest{
		BotID:             "bot1",
		ChannelType:       ChannelType,
		CredentialPayload: payload,
		Callbacks: channel.RuntimeCallbacks{
			OnEvent: func(ev channel.RuntimeEvent) { events <- ev },
		},
	})
	if err != nil {
		t.Fatalf("StartRuntime: %v", err)
	}
	return p, fc, registry, events, handle
}

func TestStartRuntimeRegistersCreds(t *testing.T) {
	_, _, registry, _, handle := startTestRuntime(t)
	defer handle.Stop()
	if got, ok := registry.Lookup("bot1"); !ok || got.AppID != "cli_x" {
		t.Fatalf("registry Lookup = %#v ok=%v", got, ok)
	}
}

func TestRuntimeP2PMessageEmitsEvent(t *testing.T) {
	_, fc, _, events, handle := startTestRuntime(t)
	defer handle.Stop()
	fc.inject(InboundMessage{MessageID: "om_1", ChatID: "oc_1", ChatType: "p2p", SenderOpenID: "ou_user", Text: "hi"})

	select {
	case ev := <-events:
		if ev.Text != "hi" || ev.MessageID != "om_1" {
			t.Fatalf("event = %#v", ev)
		}
		if ev.ReplyTarget.MetadataValue("chat_id") != "oc_1" || ev.ReplyTarget.MetadataValue("chat_type") != "p2p" {
			t.Fatalf("reply metadata = %#v", ev.ReplyTarget.Metadata)
		}
		if ev.ReplyTarget.MetadataValue("bot_id") != "bot1" {
			t.Fatalf("bot_id metadata = %q", ev.ReplyTarget.MetadataValue("bot_id"))
		}
		if ev.ReplyTarget.MetadataValue("sender_open_id") != "ou_user" {
			t.Fatalf("sender_open_id metadata = %q", ev.ReplyTarget.MetadataValue("sender_open_id"))
		}
	case <-time.After(time.Second):
		t.Fatal("expected p2p event")
	}
}

func TestRuntimeGroupWithoutMentionIgnored(t *testing.T) {
	_, fc, _, events, handle := startTestRuntime(t)
	defer handle.Stop()
	fc.inject(InboundMessage{MessageID: "om_2", ChatID: "oc_2", ChatType: "group", SenderOpenID: "ou_user", Text: "hello", MentionedOpenIDs: []string{"ou_other"}})

	select {
	case ev := <-events:
		t.Fatalf("expected no event, got %#v", ev)
	case <-time.After(150 * time.Millisecond):
		// success: nothing delivered
	}
}

func TestRuntimeGroupWithMentionEmitsEventWithReplyID(t *testing.T) {
	_, fc, _, events, handle := startTestRuntime(t)
	defer handle.Stop()
	fc.inject(InboundMessage{MessageID: "om_3", ChatID: "oc_3", ChatType: "group", SenderOpenID: "ou_user", Text: "@bot hi", MentionedOpenIDs: []string{"ou_bot"}})

	select {
	case ev := <-events:
		if ev.ReplyTarget.MetadataValue("message_id") != "om_3" || ev.ReplyTarget.MetadataValue("chat_type") != "group" {
			t.Fatalf("reply metadata = %#v", ev.ReplyTarget.Metadata)
		}
		if ev.ReplyTarget.MetadataValue("sender_open_id") != "ou_user" {
			t.Fatalf("sender_open_id metadata = %q", ev.ReplyTarget.MetadataValue("sender_open_id"))
		}
	case <-time.After(time.Second):
		t.Fatal("expected group @ event")
	}
}

func TestRuntimeStopUnregisters(t *testing.T) {
	_, _, registry, _, handle := startTestRuntime(t)
	handle.Stop()
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle did not finish")
	}
	if _, ok := registry.Lookup("bot1"); ok {
		t.Fatal("expected creds unregistered after Stop")
	}
}
