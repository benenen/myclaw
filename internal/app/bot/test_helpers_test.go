package bot

import (
	"context"

	"github.com/benenen/myclaw/internal/domain"
)

type agentCapabilityRepoStub struct {
	byID       map[string]domain.AgentCapability
	byKey      map[string]domain.AgentCapability
	list       []domain.AgentCapability
	getByIDErr error
}

func (r *agentCapabilityRepoStub) Upsert(_ context.Context, capability domain.AgentCapability) (domain.AgentCapability, error) {
	if r.byID == nil {
		r.byID = map[string]domain.AgentCapability{}
	}
	if r.byKey == nil {
		r.byKey = map[string]domain.AgentCapability{}
	}
	r.byID[capability.ID] = capability
	r.byKey[capability.Key] = capability
	found := false
	for idx := range r.list {
		if r.list[idx].Key == capability.Key {
			r.list[idx] = capability
			found = true
			break
		}
	}
	if !found {
		r.list = append(r.list, capability)
	}
	return capability, nil
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

func (r *agentCapabilityRepoStub) GetByKey(_ context.Context, key string) (domain.AgentCapability, error) {
	if capability, ok := r.byKey[key]; ok {
		return capability, nil
	}
	return domain.AgentCapability{}, domain.ErrNotFound
}

func (r *agentCapabilityRepoStub) List(context.Context) ([]domain.AgentCapability, error) {
	return append([]domain.AgentCapability(nil), r.list...), nil
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
