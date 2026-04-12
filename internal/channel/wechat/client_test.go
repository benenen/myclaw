package wechat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClientCreateBindingSession(t *testing.T) {
	var gotPath string
	var gotBotType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBotType = r.URL.Query().Get("bot_type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"qr_token_1","qrcode_url":"weixin://qr_token_1","expires_at":"2026-04-11T00:00:00Z"}`))
	}))
	defer ts.Close()

	client := NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil)
	result, err := client.CreateBindingSession(context.Background(), "bind_1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBotType != "3" {
		t.Fatalf("unexpected bot_type: %s", gotBotType)
	}
	if result.QRCode != "qr_token_1" {
		t.Fatalf("unexpected qrcode: %s", result.QRCode)
	}
}

func TestHTTPClientCreateBindingSessionReturnsErrorWhenBotTypeMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bot_type") == "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"err_msg":"missing bot_type","ret":1}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"qr_token_1","qrcode_url":"weixin://qr_token_1"}`))
	}))
	defer ts.Close()

	client := NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil)
	result, err := client.CreateBindingSession(context.Background(), "bind_1")
	if err != nil {
		t.Fatal(err)
	}
	if result.QRCode == "" {
		t.Fatal("expected qrcode")
	}
}

func TestHTTPClientCreateBindingSessionNormalizesNestedAndBase64FieldsFromDataEnvelope(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"ticket":"ticket_nested_client_1","url":"weixin://ticket_nested_client_1","qr_base64":"data:image/png;base64,client123"}}`))
	}))
	defer ts.Close()

	client := NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil)
	result, err := client.CreateBindingSession(context.Background(), "bind_nested_client_1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.Ticket != "ticket_nested_client_1" {
		t.Fatalf("unexpected ticket: %q", result.Ticket)
	}
	if result.URL != "weixin://ticket_nested_client_1" {
		t.Fatalf("unexpected url: %q", result.URL)
	}
	if result.QRCodeURL != "data:image/png;base64,client123" {
		t.Fatalf("unexpected qrcode url: %q", result.QRCodeURL)
	}
}

func TestHTTPClientGetBindingSession(t *testing.T) {
	var gotPath string
	var gotQRCode string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQRCode = r.URL.Query().Get("qrcode")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"confirmed","qrcode":"qr_token_1","openid":"wxid_1","nickname":"bot-user","expires_at":"2026-04-11T00:00:00Z"}`))
	}))
	defer ts.Close()

	client := NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil)
	result, err := client.GetBindingSession(context.Background(), "qr_token_1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_qrcode_status" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQRCode != "qr_token_1" {
		t.Fatalf("unexpected qrcode query: %s", gotQRCode)
	}
	if result.OpenID != "wxid_1" {
		t.Fatalf("unexpected openid: %s", result.OpenID)
	}
}

func TestHTTPClientGetMessagesLongPollParsesMessagesAndCursor(t *testing.T) {
	var gotPath string
	var gotAuthorizationType string
	var gotAuthorization string
	var gotWechatUIN string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorizationType = r.Header.Get("AuthorizationType")
		gotAuthorization = r.Header.Get("Authorization")
		gotWechatUIN = r.Header.Get("X-WECHAT-UIN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ret":0,
			"msgs":[
				{
					"message_id":123,
					"from_user_id":"wxid_sender",
					"create_time_ms":1710000000000,
					"item_list":[
						{"msg_id":"item_1","text_item":{"text":"你好"}},
						{"text_item":{"text":"第二行"}}
					]
				}
			],
			"get_updates_buf":"cursor_next",
			"longpolling_timeout_ms":12000
		}`))
	}))
	defer ts.Close()

	client := NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil)
	got, err := client.GetMessagesLongPoll(context.Background(), GetUpdatesOptions{Token: "test_token", WechatUIN: "dGVzdA==", Cursor: "cursor_prev", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/getupdates" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuthorizationType != "ilink_bot_token" {
		t.Fatalf("unexpected authorization type: %q", gotAuthorizationType)
	}
	if gotAuthorization != "Bearer test_token" {
		t.Fatalf("unexpected authorization: %q", gotAuthorization)
	}
	if gotWechatUIN != "dGVzdA==" {
		t.Fatalf("unexpected x-wechat-uin header: %q", gotWechatUIN)
	}
	if got.Cursor != "cursor_next" {
		t.Fatalf("unexpected cursor: %q", got.Cursor)
	}
	if got.NextTimeout != 12*time.Second {
		t.Fatalf("unexpected timeout: %s", got.NextTimeout)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("unexpected message count: %d", len(got.Messages))
	}
	if got.Messages[0].MsgID != "item_1" {
		t.Fatalf("unexpected message id: %q", got.Messages[0].MsgID)
	}
	if got.Messages[0].From != "wxid_sender" {
		t.Fatalf("unexpected sender: %q", got.Messages[0].From)
	}
	if got.Messages[0].Text != "你好\n第二行" {
		t.Fatalf("unexpected text: %q", got.Messages[0].Text)
	}
}
