package bot

import (
	"context"

	"github.com/benenen/myclaw/internal/domain"
)

type agentCapabilityRepoStub struct {
	byID       map[string]domain.AgentCapability
	getByIDErr error
}

func (r *agentCapabilityRepoStub) Upsert(context.Context, domain.AgentCapability) (domain.AgentCapability, error) {
	panic("unexpected Upsert call")
}

func (r *agentCapabilityRepoStub) GetByID(_ context.Context, id string) (domain.AgentCapability, error) {
	if r.getByIDErr != nil {
		return domain.AgentCapability{}, r.getByIDErr
	}
	if capability, ok := r.byID[id]; ok {
		return capability, nil
	}
	return domain.AgentCapability{}, domain.ErrNotFound
}

func (r *agentCapabilityRepoStub) GetByKey(context.Context, string) (domain.AgentCapability, error) {
	panic("unexpected GetByKey call")
}

func (r *agentCapabilityRepoStub) List(context.Context) ([]domain.AgentCapability, error) {
	panic("unexpected List call")
}

type botRepoStub struct {
	bot domain.Bot
}

func newBotRepoStub(bot domain.Bot) *botRepoStub {
	return &botRepoStub{bot: bot}
}

func (r *botRepoStub) Create(context.Context, domain.Bot) (domain.Bot, error) {
	panic("unexpected call")
}
func (r *botRepoStub) GetByID(_ context.Context, id string) (domain.Bot, error) {
	if r.bot.ID != id {
		return domain.Bot{}, domain.ErrNotFound
	}
	return r.bot, nil
}
func (r *botRepoStub) ListByUserID(context.Context, string) ([]domain.Bot, error) {
	panic("unexpected call")
}
func (r *botRepoStub) ListWithAccounts(context.Context) ([]domain.Bot, error) {
	panic("unexpected call")
}
func (r *botRepoStub) Update(_ context.Context, bot domain.Bot) (domain.Bot, error) {
	r.bot = bot
	return bot, nil
}
func (r *botRepoStub) DeleteByID(context.Context, string) error { panic("unexpected call") }
