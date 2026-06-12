package httpchan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/benenen/myclaw/internal/channel"
)

const ChannelType = "http"

type Provider struct {
	receiver *Receiver
}

func NewProvider(receiver *Receiver) *Provider {
	return &Provider{receiver: receiver}
}

func (p *Provider) CreateBinding(ctx context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	return channel.CreateBindingResult{
		ProviderBindingRef: req.BindingID,
		ExpiresAt:          time.Now().Add(5 * time.Minute),
	}, nil
}

func (p *Provider) RefreshBinding(ctx context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	cred, _ := json.Marshal(map[string]any{
		"channel_type": ChannelType,
	})
	return channel.RefreshBindingResult{
		ProviderStatus:    "confirmed",
		AccountUID:        "http-" + req.ProviderBindingRef,
		DisplayName:       "HTTP Channel",
		CredentialPayload: cred,
		CredentialVersion: 1,
	}, nil
}

func (p *Provider) BuildRuntimeConfig(ctx context.Context, req channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	return channel.RuntimeConfig{
		"credential_blob": map[string]any{
			"version": req.CredentialVersion,
		},
	}, nil
}

func (p *Provider) StartRuntime(ctx context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	if p.receiver == nil {
		return nil, fmt.Errorf("httpchan: receiver is nil")
	}
	p.receiver.Register(req.BotID, req.Callbacks)

	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateConnected,
		})
	}

	runtimeCtx, cancel := context.WithCancel(ctx)
	handle := &runtimeHandle{
		stopped:  make(chan struct{}),
		receiver: p.receiver,
		botID:    req.BotID,
		cancel:   cancel,
	}
	go func() {
		<-runtimeCtx.Done()
		handle.receiver.Unregister(handle.botID)
		close(handle.stopped)
	}()
	return handle, nil
}

type runtimeHandle struct {
	stopped  chan struct{}
	receiver *Receiver
	botID    string
	cancel   context.CancelFunc
}

func (h *runtimeHandle) Stop() {
	h.cancel()
}

func (h *runtimeHandle) Done() <-chan struct{} {
	return h.stopped
}

var _ channel.Provider = (*Provider)(nil)
var _ channel.RuntimeStarter = (*Provider)(nil)
