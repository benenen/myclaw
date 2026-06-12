package channel

import (
	"context"
	"fmt"

	"github.com/benenen/myclaw/internal/agent"
)

type ReplyGateway interface {
	Reply(ctx context.Context, target ReplyTarget, resp agent.Response) error
}

type MultiReplyGateway struct {
	gateways map[string]ReplyGateway
}

func NewMultiReplyGateway() *MultiReplyGateway {
	return &MultiReplyGateway{
		gateways: make(map[string]ReplyGateway),
	}
}

func (mg *MultiReplyGateway) Register(channelType string, gw ReplyGateway) {
	mg.gateways[channelType] = gw
}

func (mg *MultiReplyGateway) Reply(ctx context.Context, target ReplyTarget, resp agent.Response) error {
	gw, ok := mg.gateways[target.ChannelType]
	if !ok {
		return fmt.Errorf("channel: no reply gateway for channel type %q", target.ChannelType)
	}
	return gw.Reply(ctx, target, resp)
}
