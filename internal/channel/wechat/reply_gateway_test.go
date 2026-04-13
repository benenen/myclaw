package wechat

import (
	"context"
	"fmt"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type fakeClient struct {
	send func(context.Context, SendMessageOptions) error
}

func (f fakeClient) SendTextMessage(ctx context.Context, opts SendMessageOptions) error {
	return f.send(ctx, opts)
}

func TestReplyGatewayReply(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.ToUserID != "user-1" || opts.Text != "hello" || opts.Token != "token" || opts.WechatUIN != "uin" || opts.BaseURL != "https://wechat.example" {
			t.Fatalf("opts = %#v", opts)
		}
		return nil
	}}
	gateway := NewReplyGateway(client)
	err := gateway.Reply(context.Background(), channel.ReplyTarget{
		ChannelType: "wechat",
		RecipientID: "user-1",
		Metadata: map[string]string{
			"base_url":   "https://wechat.example",
			"token":      "token",
			"wechat_uin": "uin",
		},
	}, agent.Response{Text: "hello"})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
}

func TestReplyGatewayReplyWrapsSendFailure(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		return fmt.Errorf("send failed")
	}}
	gateway := NewReplyGateway(client)
	err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "wechat reply: send failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRequiresRecipientID(t *testing.T) {
	called := false
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		called = true
		return nil
	}})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyRecipient {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected send to be skipped")
	}
}

func TestReplyGatewayReplyRequiresBaseURL(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyBaseURL {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRequiresToken(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyToken {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRequiresWechatUIN(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url": "https://wechat.example",
		"token":    "token",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyWechatUIN {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyTrimsReplyMetadata(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.BaseURL != "https://wechat.example" || opts.Token != "token" || opts.WechatUIN != "uin" || opts.ToUserID != "user-1" {
			t.Fatalf("opts = %#v", opts)
		}
		return nil
	}}
	gateway := NewReplyGateway(client)
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: " user-1 ", Metadata: map[string]string{
		"base_url":   " https://wechat.example ",
		"token":      " token ",
		"wechat_uin": " uin ",
	}}, agent.Response{Text: "hello"}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
}

func TestValidateReplyTargetRejectsWhitespaceOnlyMetadata(t *testing.T) {
	err := validateReplyTarget(channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "   ",
		"token":      "token",
		"wechat_uin": "uin",
	}})
	if err != ErrMissingReplyBaseURL {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReplyTargetAcceptsCompleteTarget(t *testing.T) {
	if err := validateReplyTarget(channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTrimmedMetadataValue(t *testing.T) {
	target := channel.ReplyTarget{Metadata: map[string]string{"token": " token "}}
	if got := trimmedMetadataValue(target, "token"); got != "token" {
		t.Fatalf("unexpected trimmed value: %q", got)
	}
}

func TestTrimmedRecipientID(t *testing.T) {
	if got := trimmedRecipientID(channel.ReplyTarget{RecipientID: " user-1 "}); got != "user-1" {
		t.Fatalf("unexpected trimmed recipient: %q", got)
	}
}

func TestReplyGatewayReplySkipsValidationForWhitespaceOnlyText(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		t.Fatal("expected send to be skipped")
		return nil
	}})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{}, agent.Response{Text: "\n\t  "}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRejectsWhitespaceOnlyRecipient(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "   ", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyRecipient {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRejectsWhitespaceOnlyToken(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "   ",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyToken {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRejectsWhitespaceOnlyWechatUIN(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": " \t ",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyWechatUIN {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRejectsWhitespaceOnlyBaseURL(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   " \n ",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyBaseURL {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRejectsMissingMetadataMap(t *testing.T) {
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error { return nil }})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1"}, agent.Response{Text: "hello"}); err != ErrMissingReplyBaseURL {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyRejectsEmptyMetadataValuesBeforeCallingClient(t *testing.T) {
	called := false
	gateway := NewReplyGateway(fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		called = true
		return nil
	}})
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "hello"}); err != ErrMissingReplyToken {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected send to be skipped")
	}
}

func TestReplyGatewayReplyKeepsTrimmedText(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.Text != "hello" {
			t.Fatalf("unexpected text: %q", opts.Text)
		}
		return nil
	}}
	gateway := NewReplyGateway(client)
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "  hello  "}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplyDoesNotTrimInnerWhitespace(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.Text != "hello\nworld" {
			t.Fatalf("unexpected text: %q", opts.Text)
		}
		return nil
	}}
	gateway := NewReplyGateway(client)
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "\nhello\nworld\n"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReplyTargetMissingTokenPrecedesWechatUIN(t *testing.T) {
	err := validateReplyTarget(channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{"base_url": "https://wechat.example"}})
	if err != ErrMissingReplyToken {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReplyTargetMissingBaseURLPrecedesToken(t *testing.T) {
	err := validateReplyTarget(channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{"token": "token", "wechat_uin": "uin"}})
	if err != ErrMissingReplyBaseURL {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReplyTargetMissingRecipientPrecedesMetadataChecks(t *testing.T) {
	err := validateReplyTarget(channel.ReplyTarget{})
	if err != ErrMissingReplyRecipient {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTrimmedMetadataValueMissingKeyReturnsEmptyString(t *testing.T) {
	if got := trimmedMetadataValue(channel.ReplyTarget{}, "token"); got != "" {
		t.Fatalf("unexpected trimmed value: %q", got)
	}
}

func TestTrimmedRecipientIDEmptyReturnsEmptyString(t *testing.T) {
	if got := trimmedRecipientID(channel.ReplyTarget{}); got != "" {
		t.Fatalf("unexpected trimmed recipient: %q", got)
	}
}

func TestReplyGatewayReplyUsesTrimmedMetadataOnWrappedSendFailure(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.BaseURL != "https://wechat.example" || opts.Token != "token" || opts.WechatUIN != "uin" || opts.ToUserID != "user-1" {
			t.Fatalf("opts = %#v", opts)
		}
		return fmt.Errorf("send failed")
	}}
	gateway := NewReplyGateway(client)
	err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: " user-1 ", Metadata: map[string]string{
		"base_url":   " https://wechat.example ",
		"token":      " token ",
		"wechat_uin": " uin ",
	}}, agent.Response{Text: "hello"})
	if err == nil || err.Error() != "wechat reply: send failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplyGatewayReplySuppressesWhitespaceOnlyReply(t *testing.T) {
	called := false
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		called = true
		return nil
	}}
	gateway := NewReplyGateway(client)
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1"}, agent.Response{Text: "  \n\t  "}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if called {
		t.Fatal("expected send to be skipped")
	}
}

func TestReplyGatewayReplyTrimsWhitespaceBeforeSend(t *testing.T) {
	client := fakeClient{send: func(ctx context.Context, opts SendMessageOptions) error {
		if opts.Text != "hello" {
			t.Fatalf("unexpected text: %q", opts.Text)
		}
		return nil
	}}
	gateway := NewReplyGateway(client)
	if err := gateway.Reply(context.Background(), channel.ReplyTarget{RecipientID: "user-1", Metadata: map[string]string{
		"base_url":   "https://wechat.example",
		"token":      "token",
		"wechat_uin": "uin",
	}}, agent.Response{Text: "  hello\n"}); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
}
