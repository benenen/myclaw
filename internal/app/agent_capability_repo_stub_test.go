package app

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

var _ domain.AgentCapabilityRepository = (*agentCapabilityRepoStub)(nil)
