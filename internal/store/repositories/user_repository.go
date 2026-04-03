package repositories

import (
	"context"
	"time"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/store/models"
	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) FindOrCreateByExternalUserID(ctx context.Context, externalUserID string) (domain.User, error) {
	var m models.User
	err := r.db.WithContext(ctx).Where("external_user_id = ?", externalUserID).First(&m).Error
	if err == nil {
		return toDomainUser(m), nil
	}
	if err != gorm.ErrRecordNotFound {
		return domain.User{}, err
	}
	now := time.Now().UTC()
	m = models.User{
		ID:             domain.NewPrefixedID("usr"),
		ExternalUserID: externalUserID,
		Status:         "active",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return domain.User{}, err
	}
	return toDomainUser(m), nil
}

func toDomainUser(m models.User) domain.User {
	return domain.User{
		ID:             m.ID,
		ExternalUserID: m.ExternalUserID,
		Status:         m.Status,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
