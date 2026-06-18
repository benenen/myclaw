package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/logging"
)

type Provider struct {
	api      feishuAPI
	dialer   dialer
	registry *Registry
	logger   *logging.Logger
}

func NewProvider(api feishuAPI, d dialer, registry *Registry, logger *logging.Logger) *Provider {
	return &Provider{api: api, dialer: d, registry: registry, logger: logger}
}

// CreateBinding is a no-op handshake: Feishu has no QR scan, so we return the
// binding ID immediately and let the auto-confirm path call RefreshBinding.
func (p *Provider) CreateBinding(_ context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	return channel.CreateBindingResult{
		ProviderBindingRef: req.BindingID,
		ExpiresAt:          time.Now().Add(5 * time.Minute),
	}, nil
}

// RefreshBinding validates the supplied App ID/Secret, then returns a
// confirmed binding carrying the credential payload to be encrypted and
// stored on the channel account.
func (p *Provider) RefreshBinding(ctx context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	appID := strings.TrimSpace(req.Config["app_id"])
	appSecret := strings.TrimSpace(req.Config["app_secret"])
	if appID == "" || appSecret == "" {
		return channel.RefreshBindingResult{}, fmt.Errorf("feishu: app_id and app_secret are required")
	}
	info, err := p.api.ValidateApp(ctx, appID, appSecret)
	if err != nil {
		return channel.RefreshBindingResult{}, fmt.Errorf("feishu validate app: %w", err)
	}
	payload, err := json.Marshal(Credentials{AppID: appID, AppSecret: appSecret, BotOpenID: info.BotOpenID})
	if err != nil {
		return channel.RefreshBindingResult{}, fmt.Errorf("feishu marshal credentials: %w", err)
	}
	return channel.RefreshBindingResult{
		ProviderStatus:    "confirmed",
		AccountUID:        appID,
		DisplayName:       info.AppName,
		CredentialPayload: payload,
		CredentialVersion: 1,
	}, nil
}

func (p *Provider) BuildRuntimeConfig(_ context.Context, req channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	return channel.RuntimeConfig{
		"credential_blob": map[string]any{"version": req.CredentialVersion},
	}, nil
}

var _ channel.Provider = (*Provider)(nil)
