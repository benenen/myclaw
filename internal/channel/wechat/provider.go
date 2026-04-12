package wechat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/logging"
)

type Provider struct {
	client Client
	logger *logging.Logger
}

func NewProvider(client Client, logger *logging.Logger) *Provider {
	return &Provider{client: client, logger: logger}
}

func (p *Provider) CreateBinding(ctx context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	result, err := p.client.CreateBindingSession(ctx, req.BindingID)
	if err != nil {
		return channel.CreateBindingResult{}, fmt.Errorf("wechat create binding: %w", err)
	}
	qrPayload, err := createBindingQRCodePayload(result)
	if err != nil {
		return channel.CreateBindingResult{}, fmt.Errorf("wechat render qr: %w", err)
	}
	return channel.CreateBindingResult{
		ProviderBindingRef: result.providerRef(),
		QRCodePayload:      qrPayload,
		QRShareURL:         result.qrShareURL(),
		ExpiresAt:          result.normalizedExpiry(),
	}, nil
}

func createBindingQRCodePayload(result CreateSessionResult) (string, error) {
	if result.QRCodeImgContent != "" {
		return renderQRCodeDataURL(result.QRCodeImgContent)
	}
	if result.QRCodeURL != "" {
		return result.QRCodeURL, nil
	}
	if result.URL != "" {
		return result.URL, nil
	}
	return result.qrPayload(), nil
}

func renderQRCodeDataURL(payload string) (string, error) {
	png, err := qrcode.Encode(payload, qrcode.Medium, 280)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func (p *Provider) RefreshBinding(ctx context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	result, err := p.client.GetBindingSession(ctx, req.ProviderBindingRef)
	if err != nil {
		return channel.RefreshBindingResult{}, fmt.Errorf("wechat refresh binding: %w", err)
	}
	p.logger.Debug("wechat login payload", "status", result.Status, "openid", result.OpenID, "credential_payload", string(result.normalizedCredentialPayload()))
	return channel.RefreshBindingResult{
		ProviderStatus:    result.normalizedStatus(),
		QRCodePayload:     result.qrPayload(),
		ExpiresAt:         result.normalizedExpiry(),
		AccountUID:        result.accountUID(),
		DisplayName:       result.displayName(),
		AvatarURL:         result.AvatarURL,
		CredentialPayload: result.normalizedCredentialPayload(),
		CredentialVersion: result.normalizedCredentialVersion(),
		ErrorMessage:      result.ErrorMessage,
	}, nil
}

func (p *Provider) BuildRuntimeConfig(ctx context.Context, req channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	var payload map[string]any
	if err := json.Unmarshal(req.CredentialPayload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal credential payload: %w", err)
	}
	return channel.RuntimeConfig{
		"credential_blob": map[string]any{
			"version": req.CredentialVersion,
			"payload": payload,
		},
		"runtime_options": map[string]any{
			"poll_interval_seconds": 3,
		},
	}, nil
}
