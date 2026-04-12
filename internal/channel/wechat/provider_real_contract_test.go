package wechat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/benenen/myclaw/internal/channel"
)

func TestProviderCreateBindingUsesBotQRCodeEndpoint(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"qr_token_1","qrcode_url":"weixin://qr_token_1"}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_1",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.QRCodePayload != "weixin://qr_token_1" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
}

func TestProviderCreateBindingFallsBackToQRCodeWhenURLMissing(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"qr_token_2","qrcode_url":""}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_2",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.QRCodePayload != "qr_token_2" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
}

func TestProviderCreateBindingUsesQRCodeTicketWhenQRCodeMissing(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket":"ticket_1","url":"weixin://ticket_1"}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_3",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.ProviderBindingRef != "ticket_1" {
		t.Fatalf("unexpected provider binding ref: %q", result.ProviderBindingRef)
	}
	if result.QRCodePayload != "weixin://ticket_1" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
}

func TestProviderCreateBindingUsesNestedDataQRCodePayload(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"qrcode":"qr_nested_1","qrcode_url":"weixin://qr_nested_1"}}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_4",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.ProviderBindingRef != "qr_nested_1" {
		t.Fatalf("unexpected provider binding ref: %q", result.ProviderBindingRef)
	}
	if result.QRCodePayload != "weixin://qr_nested_1" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
}

func TestProviderCreateBindingUsesNestedDataTicketPayload(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"ticket":"ticket_nested_1","url":"weixin://ticket_nested_1"}}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_5",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.ProviderBindingRef != "ticket_nested_1" {
		t.Fatalf("unexpected provider binding ref: %q", result.ProviderBindingRef)
	}
	if result.QRCodePayload != "weixin://ticket_nested_1" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
}

func TestProviderCreateBindingUsesBase64ImagePayload(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"qr_base64":"data:image/png;base64,abc123","ticket":"ticket_img_1"}}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_6",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.ProviderBindingRef != "ticket_img_1" {
		t.Fatalf("unexpected provider binding ref: %q", result.ProviderBindingRef)
	}
	if result.QRCodePayload != "data:image/png;base64,abc123" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
}

func TestProviderCreateBindingPreservesBase64QRCodeURLPayload(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"qr_base64_url_1","qrcode_url":"data:image/png;base64,already_encoded"}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_base64_url_1",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.ProviderBindingRef != "qr_base64_url_1" {
		t.Fatalf("unexpected provider binding ref: %q", result.ProviderBindingRef)
	}
	if result.QRCodePayload != "data:image/png;base64,already_encoded" {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
	if result.QRShareURL != "data:image/png;base64,already_encoded" {
		t.Fatalf("unexpected qr share url: %q", result.QRShareURL)
	}
}

func TestProviderCreateBindingReturnsGeneratedQRBase64(t *testing.T) {
	const qrToken = "71ee191ef81e7d014e489500a14c87df"
	const qrImageContent = "https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=71ee191ef81e7d014e489500a14c87df&bot_type=3"

	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"qrcode":"71ee191ef81e7d014e489500a14c87df","qrcode_img_content":"https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=71ee191ef81e7d014e489500a14c87df&bot_type=3","ret":0}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	result, err := provider.CreateBinding(context.Background(), channel.CreateBindingRequest{
		BindingID:   "bind_7",
		ChannelType: "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_bot_qrcode" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if result.ProviderBindingRef != qrToken {
		t.Fatalf("unexpected provider binding ref: %q", result.ProviderBindingRef)
	}
	if !strings.HasPrefix(result.QRCodePayload, "data:image/png;base64,") {
		t.Fatalf("unexpected qr payload: %q", result.QRCodePayload)
	}
	if strings.Contains(result.QRCodePayload, qrToken) {
		t.Fatalf("qr payload should not be generated from token, got %q", result.QRCodePayload)
	}
	if rendered, err := renderQRCodeDataURL(qrImageContent); err != nil {
		t.Fatal(err)
	} else if result.QRCodePayload != rendered {
		t.Fatalf("unexpected rendered qr payload")
	}
	if result.QRShareURL != qrImageContent {
		t.Fatalf("unexpected qr share url: %q", result.QRShareURL)
	}
}

func TestHTTPClientCreateBindingSessionNormalizesNestedAndBase64Fields(t *testing.T) {
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

func TestProviderRefreshBindingUsesQRCodeStatusEndpoint(t *testing.T) {
	var gotPath string
	var gotQRCode string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQRCode = r.URL.Query().Get("qrcode")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"confirmed","openid":"wxid_1","nickname":"bot-user"}`))
	}))
	defer ts.Close()

	provider := NewProvider(NewHTTPClient(Config{ReferenceBaseURL: ts.URL}, nil), nil)
	_, err := provider.RefreshBinding(context.Background(), channel.RefreshBindingRequest{ProviderBindingRef: "qr_token_1"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/get_qrcode_status" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotQRCode != "qr_token_1" {
		t.Fatalf("unexpected qrcode: %s", gotQRCode)
	}
}
