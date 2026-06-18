package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/channel"
)

func newTestProvider(api feishuAPI) *Provider {
	return NewProvider(api, &fakeDialer{conn: newFakeConn()}, NewRegistry(), nil)
}

func TestCreateBindingReturnsRef(t *testing.T) {
	p := newTestProvider(&fakeAPI{})
	res, err := p.CreateBinding(context.Background(), channel.CreateBindingRequest{BindingID: "bind_1", ChannelType: ChannelType})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}
	if res.ProviderBindingRef != "bind_1" {
		t.Fatalf("ProviderBindingRef = %q, want bind_1", res.ProviderBindingRef)
	}
	if res.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt should be set")
	}
}

func TestRefreshBindingValidatesAndConfirms(t *testing.T) {
	api := &fakeAPI{validateInfo: AppInfo{AppName: "My App", BotOpenID: "ou_bot"}}
	p := newTestProvider(api)

	res, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ChannelType: ChannelType,
		Config:      map[string]string{"app_id": "cli_x", "app_secret": "secret"},
	})
	if err != nil {
		t.Fatalf("RefreshBinding: %v", err)
	}
	if api.validatedAppID != "cli_x" || api.validatedSecret != "secret" {
		t.Fatalf("ValidateApp got app=%q secret=%q", api.validatedAppID, api.validatedSecret)
	}
	if res.ProviderStatus != "confirmed" {
		t.Fatalf("status = %q, want confirmed", res.ProviderStatus)
	}
	if res.AccountUID != "cli_x" || res.DisplayName != "My App" {
		t.Fatalf("AccountUID=%q DisplayName=%q", res.AccountUID, res.DisplayName)
	}
	var creds Credentials
	if err := json.Unmarshal(res.CredentialPayload, &creds); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if creds.AppID != "cli_x" || creds.AppSecret != "secret" || creds.BotOpenID != "ou_bot" {
		t.Fatalf("credential payload = %#v", creds)
	}
}

func TestRefreshBindingMissingCredentials(t *testing.T) {
	p := newTestProvider(&fakeAPI{})
	_, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ChannelType: ChannelType,
		Config:      map[string]string{"app_id": "cli_x"},
	})
	if err == nil {
		t.Fatal("expected error for missing app_secret")
	}
}

func TestRefreshBindingValidateError(t *testing.T) {
	p := newTestProvider(&fakeAPI{validateErr: errors.New("bad creds")})
	_, err := p.RefreshBinding(context.Background(), channel.RefreshBindingRequest{
		ChannelType: ChannelType,
		Config:      map[string]string{"app_id": "cli_x", "app_secret": "secret"},
	})
	if err == nil {
		t.Fatal("expected error when validation fails")
	}
}
