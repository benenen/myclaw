package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/logging"
)

func withSendMessageDefaultTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()

	original := defaultSendMessageTimeout
	defaultSendMessageTimeout = timeout
	t.Cleanup(func() {
		defaultSendMessageTimeout = original
	})
}

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

func TestHTTPClientGetMessagesLongPollReturnsBodyOnNon200NonJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("gateway exploded"))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	_, err := client.GetMessagesLongPoll(context.Background(), GetUpdatesOptions{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "decode getupdates response") {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if !strings.Contains(err.Error(), "status=502") || !strings.Contains(err.Error(), `body="gateway exploded"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientGetMessagesLongPollReturnsEnvelopeDetailsOnNon200JSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"ret":123,"errcode":40013,"errmsg":"invalid cursor"}`))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	got, err := client.GetMessagesLongPoll(context.Background(), GetUpdatesOptions{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error")
	}
	if got.Ret != 123 || got.ErrCode != 40013 || got.ErrMsg != "invalid cursor" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if !strings.Contains(err.Error(), "status=502") || !strings.Contains(err.Error(), "ret=123") || !strings.Contains(err.Error(), "errcode=40013") || !strings.Contains(err.Error(), `errmsg="invalid cursor"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessage(t *testing.T) {
	requestErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			requestErrCh <- fmt.Errorf("path = %s", r.URL.Path)
			return
		}
		if got := r.Header.Get("AuthorizationType"); got != "ilink_bot_token" {
			requestErrCh <- fmt.Errorf("AuthorizationType = %q", got)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			requestErrCh <- fmt.Errorf("Authorization = %q", got)
			return
		}
		if got := r.Header.Get("X-WECHAT-UIN"); got != "uin" {
			requestErrCh <- fmt.Errorf("X-WECHAT-UIN = %q", got)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			requestErrCh <- fmt.Errorf("decode body: %v", err)
			return
		}
		msg, ok := body["msg"].(map[string]any)
		if !ok {
			requestErrCh <- fmt.Errorf("msg = %#v", body["msg"])
			return
		}
		items, ok := msg["item_list"].([]any)
		if !ok || len(items) != 1 {
			requestErrCh <- fmt.Errorf("item_list = %#v", msg["item_list"])
			return
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			requestErrCh <- fmt.Errorf("item = %#v", items[0])
			return
		}
		textItem, ok := item["text_item"].(map[string]any)
		if !ok {
			requestErrCh <- fmt.Errorf("text_item = %#v", item["text_item"])
			return
		}
		baseInfo, ok := body["base_info"].(map[string]any)
		if !ok {
			requestErrCh <- fmt.Errorf("base_info = %#v", body["base_info"])
			return
		}
		if msg["to_user_id"] != "user-1" || msg["message_type"] != float64(2) || msg["message_state"] != float64(2) || textItem["text"] != "hello" || baseInfo["channel_version"] != "1.0.0" {
			requestErrCh <- fmt.Errorf("body = %#v", body)
			return
		}
		if _, ok := msg["client_id"].(string); !ok {
			requestErrCh <- fmt.Errorf("client_id = %#v", msg["client_id"])
			return
		}
		requestErrCh <- nil
		_, _ = w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	if err := client.SendTextMessage(context.Background(), SendMessageOptions{Token: "token", WechatUIN: "uin", ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"}); err != nil {
		t.Fatalf("SendTextMessage() error = %v", err)
	}
	if err := <-requestErrCh; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientSendTextMessageRequiresContextToken(t *testing.T) {
	client := &HTTPClient{baseURL: "http://127.0.0.1:1", authToken: "token", client: &http.Client{}, logger: logging.New("debug")}

	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessageIncludesContextToken(t *testing.T) {
	requestErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			requestErrCh <- fmt.Errorf("decode body: %v", err)
			return
		}
		msg, ok := body["msg"].(map[string]any)
		if !ok {
			requestErrCh <- fmt.Errorf("msg = %#v", body["msg"])
			return
		}
		if msg["context_token"] != "ctx-1" {
			requestErrCh <- fmt.Errorf("context_token = %#v", msg["context_token"])
			return
		}
		requestErrCh <- nil
		_, _ = w.Write([]byte(`{"ret":0,"errcode":0}`))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	if err := client.SendTextMessage(context.Background(), SendMessageOptions{Token: "token", WechatUIN: "uin", ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"}); err != nil {
		t.Fatalf("SendTextMessage() error = %v", err)
	}
	if err := <-requestErrCh; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientSendTextMessageSetsDefaultTimeoutWithoutDeadline(t *testing.T) {
	withSendMessageDefaultTimeout(t, 100*time.Millisecond)

	client := &HTTPClient{
		baseURL:   "http://127.0.0.1:1",
		authToken: "token",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				return nil, fmt.Errorf("missing deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > defaultSendMessageTimeout {
				return nil, fmt.Errorf("unexpected deadline remaining: %s", remaining)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ret":0,"errcode":0}`)),
			}, nil
		})},
		logger: logging.New("debug"),
	}

	if err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"}); err != nil {
		t.Fatalf("SendTextMessage() error = %v", err)
	}
}

func TestHTTPClientSendTextMessageReturnsTransportErrorWhenDefaultTimeoutExpires(t *testing.T) {
	withSendMessageDefaultTimeout(t, 100*time.Millisecond)

	client := &HTTPClient{
		baseURL:   "http://127.0.0.1:1",
		authToken: "token",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				return nil, fmt.Errorf("missing deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > defaultSendMessageTimeout {
				return nil, fmt.Errorf("unexpected deadline remaining: %s", remaining)
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})},
		logger: logging.New("debug"),
	}

	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessageReturnsTransportErrorWhenDefaultTimeoutAlreadyExpired(t *testing.T) {
	client := &HTTPClient{
		baseURL:   "http://127.0.0.1:1",
		authToken: "token",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})},
		logger: logging.New("debug"),
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := client.SendTextMessage(ctx, SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextWithDefaultTimeoutAddsDeadlineWhenMissing(t *testing.T) {
	ctx, cancel := contextWithDefaultTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("unexpected remaining time: %s", remaining)
	}
}

func TestContextWithDefaultTimeoutPreservesExistingDeadline(t *testing.T) {
	original, originalCancel := context.WithTimeout(context.Background(), time.Second)
	defer originalCancel()

	ctx, cancel := contextWithDefaultTimeout(original, 30*time.Second)
	defer cancel()

	if ctx != original {
		t.Fatal("expected original context")
	}
}

func TestContextWithDefaultTimeoutCancelsDerivedContext(t *testing.T) {
	ctx, cancel := contextWithDefaultTimeout(context.Background(), time.Second)
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatalf("expected canceled context, got %v", ctx.Err())
	}
}

func TestHTTPClientSendTextMessageUsesDefaultTimeoutWhenTransportWaitsForCancellation(t *testing.T) {
	withSendMessageDefaultTimeout(t, 100*time.Millisecond)

	client := &HTTPClient{
		baseURL:   "http://127.0.0.1:1",
		authToken: "token",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				return nil, fmt.Errorf("missing deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > defaultSendMessageTimeout {
				return nil, fmt.Errorf("unexpected deadline remaining: %s", remaining)
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})},
		logger: logging.New("debug"),
	}

	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessagePreservesExistingDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := &HTTPClient{
		baseURL:   "http://127.0.0.1:1",
		authToken: "token",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				return nil, fmt.Errorf("missing deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > 2*time.Second {
				return nil, fmt.Errorf("unexpected deadline remaining: %s", remaining)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ret":0,"errcode":0}`)),
			}, nil
		})},
		logger: logging.New("debug"),
	}

	if err := client.SendTextMessage(ctx, SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"}); err != nil {
		t.Fatalf("SendTextMessage() error = %v", err)
	}
}

func TestHTTPClientSendTextMessageReturnsHTTPErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway exploded", http.StatusBadGateway)
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "status=502") || !strings.Contains(err.Error(), "gateway exploded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessageReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ret":0,"errcode":40013,"errmsg":"invalid recipient"}`))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "errcode=40013") || !strings.Contains(err.Error(), `errmsg="invalid recipient"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessageReturnsBodyOnNonJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "decode sendmsg response") {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if !strings.Contains(err.Error(), "status=500") || !strings.Contains(err.Error(), `body="not-json"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClientSendTextMessageReturnsEnvelopeDetailsOnNon200JSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"ret":123,"errcode":40013,"errmsg":"invalid recipient"}`))
	}))
	defer server.Close()

	client := &HTTPClient{baseURL: server.URL, authToken: "token", client: server.Client(), logger: logging.New("debug")}
	err := client.SendTextMessage(context.Background(), SendMessageOptions{ToUserID: "user-1", Text: "hello", ContextToken: "ctx-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "status=502") || !strings.Contains(err.Error(), "ret=123") || !strings.Contains(err.Error(), "errcode=40013") || !strings.Contains(err.Error(), `errmsg="invalid recipient"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDecodeSendMessageResponseReturnsBodyOnDecodeError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("not-json")),
	}

	err := decodeSendMessageResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `body="not-json"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeJSONResponseIncludesBodyOnNon200(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader("bad request body")),
	}

	_, err := decodeJSONResponse[map[string]any](resp, "action")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `body="bad request body"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeJSONResponseIncludesBodyOnDecodeFailure(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("not-json")),
	}

	_, err := decodeJSONResponse[map[string]any](resp, "action")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `body="not-json"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeSendMessageResponseReturnsAPIErrorWithBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"ret":1,"errcode":2,"errmsg":"boom"}`)),
	}

	err := decodeSendMessageResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "errcode=2") || !strings.Contains(err.Error(), `body="{\"ret\":1,\"errcode\":2,\"errmsg\":\"boom\"}"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
