package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/channel"
)

type FakeProvider struct {
	mu              sync.Mutex
	states          map[string]*fakeBindingState
	runtimeStarted  map[string]bool
}

type fakeBindingState struct {
	status            string
	qrCodePayload     string
	expiresAt         time.Time
	accountUID        string
	displayName       string
	avatarURL         string
	credentialPayload []byte
	credentialVersion int
	errorMessage      string
}

func NewFakeProvider() *FakeProvider {
	return &FakeProvider{
		states:         make(map[string]*fakeBindingState),
		runtimeStarted: make(map[string]bool),
	}
}

func (p *FakeProvider) CreateBinding(_ context.Context, req channel.CreateBindingRequest) (channel.CreateBindingResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ref := "wxbind_" + req.BindingID
	p.states[ref] = &fakeBindingState{
		status:        "qr_ready",
		qrCodePayload: "weixin://fake_qr_" + req.BindingID,
		expiresAt:     time.Now().Add(5 * time.Minute),
	}

	return channel.CreateBindingResult{
		ProviderBindingRef: ref,
		QRCodePayload:      p.states[ref].qrCodePayload,
		ExpiresAt:          p.states[ref].expiresAt,
	}, nil
}

func (p *FakeProvider) RefreshBinding(_ context.Context, req channel.RefreshBindingRequest) (channel.RefreshBindingResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.states[req.ProviderBindingRef]
	if !ok {
		return channel.RefreshBindingResult{
			ProviderStatus: "expired",
			ErrorMessage:   "binding not found",
		}, nil
	}

	return channel.RefreshBindingResult{
		ProviderStatus:    state.status,
		QRCodePayload:     state.qrCodePayload,
		ExpiresAt:         state.expiresAt,
		AccountUID:        state.accountUID,
		DisplayName:       state.displayName,
		AvatarURL:         state.avatarURL,
		CredentialPayload: state.credentialPayload,
		CredentialVersion: state.credentialVersion,
		ErrorMessage:      state.errorMessage,
	}, nil
}

func (p *FakeProvider) BuildRuntimeConfig(_ context.Context, req channel.BuildRuntimeConfigRequest) (channel.RuntimeConfig, error) {
	var payload map[string]any
	if req.CredentialPayload != nil {
		if err := json.Unmarshal(req.CredentialPayload, &payload); err != nil {
			return nil, fmt.Errorf("unmarshal credential payload: %w", err)
		}
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

func (p *FakeProvider) StartRuntime(_ context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	p.mu.Lock()
	p.runtimeStarted[req.BotID] = true
	p.mu.Unlock()

	handle := &fakeRuntimeHandle{done: make(chan struct{})}
	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{
			BotID:       req.BotID,
			ChannelType: req.ChannelType,
			State:       channel.RuntimeStateConnected,
		})
	}
	return handle, nil
}

func (p *FakeProvider) RuntimeStarted(botID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runtimeStarted[botID]
}

func (p *FakeProvider) GetMessages(_ context.Context, _ string) ([]Message, error) {
	return []Message{
		{
			MsgID:   "msg_fake_1",
			MsgType: "text",
			From:    "wxid_fake",
			Text:    "fake inbound message",
			Created: time.Now().Unix(),
		},
	}, nil
}

type fakeRuntimeHandle struct {
	done chan struct{}
	once sync.Once
}

func (h *fakeRuntimeHandle) Stop() {
	h.once.Do(func() {
		close(h.done)
	})
}

func (h *fakeRuntimeHandle) Done() <-chan struct{} {
	return h.done
}

// SimulateConfirm simulates a successful login for testing.
func (p *FakeProvider) SimulateConfirm(providerBindingRef string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.states[providerBindingRef]
	if !ok {
		return
	}
	state.status = "confirmed"
	state.accountUID = "wxid_fake_user"
	state.displayName = "Fake User"
	state.avatarURL = "https://example.com/avatar.png"
	state.credentialPayload, _ = json.Marshal(map[string]any{
		"wechat_session": map[string]string{"token": "fake_token"},
		"device":         map[string]string{"id": "fake_device"},
	})
	state.credentialVersion = 1
}

var _ channel.RuntimeStarter = (*FakeProvider)(nil)
