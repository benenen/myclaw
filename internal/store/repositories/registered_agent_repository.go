package repositories

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RegisteredAgentRepository struct {
	db *gorm.DB
}

func NewRegisteredAgentRepository(db *gorm.DB) *RegisteredAgentRepository {
	return &RegisteredAgentRepository{db: db}
}

func (r *RegisteredAgentRepository) Upsert(ctx context.Context, a domain.RegisteredAgent) (domain.RegisteredAgent, error) {
	now := time.Now().UTC()
	m := models.RegisteredAgent{
		ID:              a.ID,
		Name:            a.Name,
		Description:     a.Description,
		Kind:            a.Kind,
		BotID:           a.BotID,
		Endpoint:        a.Endpoint,
		AuthToken:       a.AuthToken,
		Health:          a.Health,
		LastHeartbeatAt: a.LastHeartbeat,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"description", "kind", "bot_id", "endpoint", "auth_token", "health", "last_heartbeat_at", "updated_at"}),
	}).Create(&m).Error; err != nil {
		return domain.RegisteredAgent{}, err
	}
	return r.GetByName(ctx, a.Name)
}

func (r *RegisteredAgentRepository) GetByID(ctx context.Context, id string) (domain.RegisteredAgent, error) {
	var m models.RegisteredAgent
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.RegisteredAgent{}, domain.ErrNotFound
		}
		return domain.RegisteredAgent{}, err
	}
	return toDomainRegisteredAgent(m), nil
}

func (r *RegisteredAgentRepository) GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error) {
	var m models.RegisteredAgent
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.RegisteredAgent{}, domain.ErrNotFound
		}
		return domain.RegisteredAgent{}, err
	}
	return toDomainRegisteredAgent(m), nil
}

func (r *RegisteredAgentRepository) List(ctx context.Context) ([]domain.RegisteredAgent, error) {
	var rows []models.RegisteredAgent
	if err := r.db.WithContext(ctx).Order("name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.RegisteredAgent, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDomainRegisteredAgent(row))
	}
	return items, nil
}

func (r *RegisteredAgentRepository) DeleteByID(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.RegisteredAgent{}).Error
}

func toDomainRegisteredAgent(m models.RegisteredAgent) domain.RegisteredAgent {
	return domain.RegisteredAgent{
		ID:            m.ID,
		Name:          m.Name,
		Description:   m.Description,
		Kind:          m.Kind,
		BotID:         m.BotID,
		Endpoint:      m.Endpoint,
		AuthToken:     m.AuthToken,
		Health:        m.Health,
		LastHeartbeat: m.LastHeartbeatAt,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

var _ domain.RegisteredAgentRepository = (*RegisteredAgentRepository)(nil)
