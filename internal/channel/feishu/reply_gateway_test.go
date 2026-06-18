package feishu

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

func p2pTarget() channel.ReplyTarget {
	return channel.ReplyTarget{
		ChannelType: ChannelType,
		RecipientID: "oc_1",
		Metadata:    map[string]string{"bot_id": "bot1", "chat_id": "oc_1", "chat_type": "p2p", "message_id": "om_1"},
	}
}

func groupTarget() channel.ReplyTarget {
	return channel.ReplyTarget{
		ChannelType: ChannelType,
		RecipientID: "oc_2",
		Metadata:    map[string]string{"bot_id": "bot1", "chat_id": "oc_2", "chat_type": "group", "message_id": "om_2"},
	}
}

func TestReplyP2PSendsToChatNoReplyID(t *testing.T) {
	api := &fakeAPI{}
	registry := NewRegistry()
	registry.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s"})
	g := NewReplyGateway(api, registry)

	if err := g.Reply(context.Background(), p2pTarget(), agent.Response{Text: "hello"}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(api.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(api.sent))
	}
	if api.sent[0].params.ChatID != "oc_1" || api.sent[0].params.ReplyMessageID != "" || api.sent[0].params.Text != "hello" {
		t.Fatalf("params = %#v", api.sent[0].params)
	}
	if api.sent[0].creds.AppID != "cli_x" {
		t.Fatalf("creds = %#v", api.sent[0].creds)
	}
}

func TestReplyGroupRepliesToMessage(t *testing.T) {
	api := &fakeAPI{}
	registry := NewRegistry()
	registry.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s"})
	g := NewReplyGateway(api, registry)

	if err := g.Reply(context.Background(), groupTarget(), agent.Response{Text: "yo"}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if api.sent[0].params.ReplyMessageID != "om_2" || api.sent[0].params.ChatID != "oc_2" {
		t.Fatalf("params = %#v", api.sent[0].params)
	}
}

func TestReplyBotNotActiveErrors(t *testing.T) {
	g := NewReplyGateway(&fakeAPI{}, NewRegistry())
	if err := g.Reply(context.Background(), p2pTarget(), agent.Response{Text: "hello"}); err == nil {
		t.Fatal("expected error when bot not registered")
	}
}

func TestReplyEmptyTextNoop(t *testing.T) {
	api := &fakeAPI{}
	registry := NewRegistry()
	registry.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s"})
	g := NewReplyGateway(api, registry)

	if err := g.Reply(context.Background(), p2pTarget(), agent.Response{Text: "   "}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(api.sent) != 0 {
		t.Fatalf("expected no send for empty text, got %d", len(api.sent))
	}
}
