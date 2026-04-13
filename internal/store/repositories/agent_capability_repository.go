package repositories

import (
	"context"
	"encoding/json"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentCapabilityRepository struct {
	db *gorm.DB
}

func NewAgentCapabilityRepository(db *gorm.DB) *AgentCapabilityRepository {
	return &AgentCapabilityRepository{db: db}
}

func (r *AgentCapabilityRepository) Upsert(ctx context.Context, capability domain.AgentCapability) (domain.AgentCapability, error) {
	now := time.Now().UTC()
	argsJSON, err := json.Marshal(capability.Args)
	if err != nil {
		return domain.AgentCapability{}, err
	}
	modesJSON, err := json.Marshal(capability.SupportedModes)
	if err != nil {
		return domain.AgentCapability{}, err
	}
	m := models.AgentCapability{
		ID:                 capability.ID,
		Key:                capability.Key,
		Label:              capability.Label,
		Command:            capability.Command,
		ArgsJSON:           string(argsJSON),
		SupportedModesJSON: string(modesJSON),
		Available:          capability.Available,
		DetectionSource:    capability.DetectionSource,
		LastDetectedAt:     capability.LastDetectedAt,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "command", "args_json", "supported_modes_json", "available", "detection_source", "last_detected_at", "updated_at"}),
	}).Create(&m).Error; err != nil {
		return domain.AgentCapability{}, err
	}
	return r.GetByKey(ctx, capability.Key)
}

func (r *AgentCapabilityRepository) GetByID(ctx context.Context, id string) (domain.AgentCapability, error) {
	var m models.AgentCapability
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.AgentCapability{}, domain.ErrNotFound
		}
		return domain.AgentCapability{}, err
	}
	return toDomainAgentCapability(m)
}

func (r *AgentCapabilityRepository) GetByKey(ctx context.Context, key string) (domain.AgentCapability, error) {
	var m models.AgentCapability
	if err := r.db.WithContext(ctx).Where("key = ?", key).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.AgentCapability{}, domain.ErrNotFound
		}
		return domain.AgentCapability{}, err
	}
	return toDomainAgentCapability(m)
}

func (r *AgentCapabilityRepository) List(ctx context.Context) ([]domain.AgentCapability, error) {
	var rows []models.AgentCapability
	if err := r.db.WithContext(ctx).Order("key asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.AgentCapability, 0, len(rows))
	for _, row := range rows {
		item, err := toDomainAgentCapability(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func toDomainAgentCapability(m models.AgentCapability) (domain.AgentCapability, error) {
	var args []string
	if err := json.Unmarshal([]byte(m.ArgsJSON), &args); err != nil {
		return domain.AgentCapability{}, err
	}
	var supportedModes []string
	if err := json.Unmarshal([]byte(m.SupportedModesJSON), &supportedModes); err != nil {
		return domain.AgentCapability{}, err
	}
	return domain.AgentCapability{
		ID:              m.ID,
		Key:             m.Key,
		Label:           m.Label,
		Command:         m.Command,
		Args:            args,
		SupportedModes:  supportedModes,
		Available:       m.Available,
		DetectionSource: m.DetectionSource,
		LastDetectedAt:  m.LastDetectedAt,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}, nil
}

var _ domain.AgentCapabilityRepository = (*AgentCapabilityRepository)(nil)
