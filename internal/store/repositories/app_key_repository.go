package repositories

import (
	"context"
	"time"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/store/models"
	"gorm.io/gorm"
)

type AppKeyRepository struct {
	db *gorm.DB
}

func NewAppKeyRepository(db *gorm.DB) *AppKeyRepository {
	return &AppKeyRepository{db: db}
}

func (r *AppKeyRepository) Create(ctx context.Context, key domain.AppKey) (domain.AppKey, error) {
	now := time.Now().UTC()
	m := models.AppKey{
		ID:               key.ID,
		UserID:           key.UserID,
		ChannelAccountID: key.ChannelAccountID,
		AppKeyHash:       key.AppKeyHash,
		AppKeyPrefix:     key.AppKeyPrefix,
		Status:           key.Status,
		CreatedAt:        now,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return domain.AppKey{}, err
	}
	return toDomainAppKey(m), nil
}

func (r *AppKeyRepository) FindByHash(ctx context.Context, hash string) (domain.AppKey, error) {
	var m models.AppKey
	if err := r.db.WithContext(ctx).Where("app_key_hash = ?", hash).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.AppKey{}, domain.ErrNotFound
		}
		return domain.AppKey{}, err
	}
	return toDomainAppKey(m), nil
}

func (r *AppKeyRepository) FindActiveByChannelAccountID(ctx context.Context, channelAccountID string) (domain.AppKey, error) {
	var m models.AppKey
	if err := r.db.WithContext(ctx).Where("channel_account_id = ? AND status = ?", channelAccountID, "active").First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.AppKey{}, domain.ErrNotFound
		}
		return domain.AppKey{}, err
	}
	return toDomainAppKey(m), nil
}

func (r *AppKeyRepository) DisableByID(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&models.AppKey{}).
		Where("id = ? AND status = ?", id, "active").
		Updates(map[string]any{"status": "disabled", "disabled_at": now}).Error
}

func (r *AppKeyRepository) DisableByChannelAccountID(ctx context.Context, channelAccountID string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&models.AppKey{}).
		Where("channel_account_id = ? AND status = ?", channelAccountID, "active").
		Updates(map[string]any{"status": "disabled", "disabled_at": now}).Error
}

func (r *AppKeyRepository) UpdateLastUsedAt(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&models.AppKey{}).
		Where("id = ?", id).
		Update("last_used_at", now).Error
}

func (r *AppKeyRepository) HasActiveByChannelAccountID(ctx context.Context, channelAccountID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.AppKey{}).
		Where("channel_account_id = ? AND status = ?", channelAccountID, "active").
		Count(&count).Error
	return count > 0, err
}

func toDomainAppKey(m models.AppKey) domain.AppKey {
	return domain.AppKey{
		ID:               m.ID,
		UserID:           m.UserID,
		ChannelAccountID: m.ChannelAccountID,
		AppKeyHash:       m.AppKeyHash,
		AppKeyPrefix:     m.AppKeyPrefix,
		Status:           m.Status,
		LastUsedAt:       m.LastUsedAt,
		CreatedAt:        m.CreatedAt,
		DisabledAt:       m.DisabledAt,
	}
}
