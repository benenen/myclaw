package bot

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

var (
	ErrBotCLIConfigMissing   = errors.New("bot cli config missing")
	ErrBotCLIUnavailable     = errors.New("bot cli unavailable")
	ErrBotCLIUnsupportedMode = errors.New("bot cli mode unsupported")
)

type BotCLIResolverConfig struct {
	Timeout time.Duration
}

type BotCLIResolver struct {
	bots         domain.BotRepository
	capabilities domain.AgentCapabilityRepository
	timeout      time.Duration
}

func NewBotCLIResolver(bots domain.BotRepository, capabilities domain.AgentCapabilityRepository, cfg BotCLIResolverConfig) *BotCLIResolver {
	return &BotCLIResolver{bots: bots, capabilities: capabilities, timeout: cfg.Timeout}
}

func (r *BotCLIResolver) Resolve(ctx context.Context, botID string) (agent.Spec, error) {
	bot, err := r.bots.GetByID(ctx, botID)
	if err != nil {
		return agent.Spec{}, err
	}
	if bot.AgentCapabilityID == "" || bot.AgentMode == "" {
		return agent.Spec{}, ErrBotCLIConfigMissing
	}
	capability, err := r.capabilities.GetByID(ctx, bot.AgentCapabilityID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return agent.Spec{}, ErrBotCLIConfigMissing
		}
		return agent.Spec{}, err
	}
	if !capability.Available {
		return agent.Spec{}, ErrBotCLIUnavailable
	}
	if !slices.Contains(capability.SupportedModes, bot.AgentMode) {
		return agent.Spec{}, ErrBotCLIUnsupportedMode
	}
	if capability.Command == "" {
		return agent.Spec{}, ErrBotCLIConfigMissing
	}
	return agent.Spec{
		Type:    bot.AgentMode,
		Command: capability.Command,
		Args:    append([]string(nil), capability.Args...),
		Timeout: r.timeout,
	}, nil
}
