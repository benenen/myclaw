package channel

import (
	"context"
	"fmt"
)

type MultiProvider struct {
	providers map[string]Provider
	starters  map[string]RuntimeStarter
}

func NewMultiProvider() *MultiProvider {
	return &MultiProvider{
		providers: make(map[string]Provider),
		starters:  make(map[string]RuntimeStarter),
	}
}

func (mp *MultiProvider) Register(channelType string, p Provider, s RuntimeStarter) {
	if p != nil {
		mp.providers[channelType] = p
	}
	if s != nil {
		mp.starters[channelType] = s
	} else if rts, ok := p.(RuntimeStarter); ok {
		mp.starters[channelType] = rts
	}
}

func (mp *MultiProvider) CreateBinding(ctx context.Context, req CreateBindingRequest) (CreateBindingResult, error) {
	p, ok := mp.providers[req.ChannelType]
	if !ok {
		return CreateBindingResult{}, fmt.Errorf("channel: unsupported channel type %q", req.ChannelType)
	}
	return p.CreateBinding(ctx, req)
}

func (mp *MultiProvider) RefreshBinding(ctx context.Context, req RefreshBindingRequest) (RefreshBindingResult, error) {
	if req.ChannelType == "" {
		return RefreshBindingResult{}, fmt.Errorf("channel: channel_type is required for refresh")
	}
	p, ok := mp.providers[req.ChannelType]
	if !ok {
		return RefreshBindingResult{}, fmt.Errorf("channel: unsupported channel type %q", req.ChannelType)
	}
	return p.RefreshBinding(ctx, req)
}

func (mp *MultiProvider) BuildRuntimeConfig(ctx context.Context, req BuildRuntimeConfigRequest) (RuntimeConfig, error) {
	if req.ChannelType == "" {
		return nil, fmt.Errorf("channel: channel_type is required for build config")
	}
	p, ok := mp.providers[req.ChannelType]
	if !ok {
		return nil, fmt.Errorf("channel: unsupported channel type %q", req.ChannelType)
	}
	return p.BuildRuntimeConfig(ctx, req)
}

func (mp *MultiProvider) StartRuntime(ctx context.Context, req StartRuntimeRequest) (RuntimeHandle, error) {
	s, ok := mp.starters[req.ChannelType]
	if !ok {
		return nil, fmt.Errorf("channel: unsupported channel type %q", req.ChannelType)
	}
	return s.StartRuntime(ctx, req)
}

var _ Provider = (*MultiProvider)(nil)
var _ RuntimeStarter = (*MultiProvider)(nil)
