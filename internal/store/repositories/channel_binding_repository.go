package repositories

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
)

type ChannelBindingRepository struct {
	db *gorm.DB
}

func NewChannelBindingRepository(db *gorm.DB) *ChannelBindingRepository {
	return &ChannelBindingRepository{db: db}
}

func (r *ChannelBindingRepository) Create(ctx context.Context, binding domain.ChannelBinding) (domain.ChannelBinding, error) {
	now := time.Now().UTC()
	m := models.ChannelBinding{
		ID:                 binding.ID,
		BotID:              binding.BotID,
		UserID:             binding.UserID,
		ChannelType:        binding.ChannelType,
		Status:             binding.Status,
		ProviderBindingRef: binding.ProviderBindingRef,
		QRCodePayload:      binding.QRCodePayload,
		ExpiresAt:          binding.ExpiresAt,
		ErrorMessage:       binding.ErrorMessage,
		ChannelAccountID:   binding.ChannelAccountID,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return domain.ChannelBinding{}, err
	}
	return toDomainChannelBinding(m), nil
}

func (r *ChannelBindingRepository) GetByID(ctx context.Context, id string) (domain.ChannelBinding, error) {
	var m models.ChannelBinding
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.ChannelBinding{}, domain.ErrNotFound
		}
		return domain.ChannelBinding{}, err
	}
	return toDomainChannelBinding(m), nil
}

func (r *ChannelBindingRepository) Update(ctx context.Context, binding domain.ChannelBinding) (domain.ChannelBinding, error) {
	now := time.Now().UTC()
	m := models.ChannelBinding{
		ID:                 binding.ID,
		BotID:              binding.BotID,
		UserID:             binding.UserID,
		ChannelType:        binding.ChannelType,
		Status:             binding.Status,
		ProviderBindingRef: binding.ProviderBindingRef,
		QRCodePayload:      binding.QRCodePayload,
		ExpiresAt:          binding.ExpiresAt,
		ErrorMessage:       binding.ErrorMessage,
		ChannelAccountID:   binding.ChannelAccountID,
		CreatedAt:          binding.CreatedAt,
		UpdatedAt:          now,
		FinishedAt:         binding.FinishedAt,
	}
	if err := r.db.WithContext(ctx).Save(&m).Error; err != nil {
		return domain.ChannelBinding{}, err
	}
	return toDomainChannelBinding(m), nil
}

func (r *ChannelBindingRepository) DeleteByBotID(ctx context.Context, botID string) error {
	return r.db.WithContext(ctx).Where("bot_id = ?", botID).Delete(&models.ChannelBinding{}).Error
}

func toDomainChannelBinding(m models.ChannelBinding) domain.ChannelBinding {
	return domain.ChannelBinding{
		ID:                 m.ID,
		BotID:              m.BotID,
		UserID:             m.UserID,
		ChannelType:        m.ChannelType,
		Status:             m.Status,
		ProviderBindingRef: m.ProviderBindingRef,
		QRCodePayload:      m.QRCodePayload,
		ExpiresAt:          m.ExpiresAt,
		ErrorMessage:       m.ErrorMessage,
		ChannelAccountID:   m.ChannelAccountID,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		FinishedAt:         m.FinishedAt,
	}
}
