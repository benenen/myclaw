package repositories

import (
	"context"
	"time"

	"github.com/benenen/channel-plugin/internal/domain"
	"github.com/benenen/channel-plugin/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChannelAccountRepository struct {
	db *gorm.DB
}

func NewChannelAccountRepository(db *gorm.DB) *ChannelAccountRepository {
	return &ChannelAccountRepository{db: db}
}

func (r *ChannelAccountRepository) Upsert(ctx context.Context, account domain.ChannelAccount) (domain.ChannelAccount, error) {
	now := time.Now().UTC()
	m := models.ChannelAccount{
		ID:                   account.ID,
		UserID:               account.UserID,
		ChannelType:          account.ChannelType,
		AccountUID:           account.AccountUID,
		DisplayName:          account.DisplayName,
		AvatarURL:            account.AvatarURL,
		CredentialCiphertext: account.CredentialCiphertext,
		CredentialVersion:    account.CredentialVersion,
		LastBoundAt:          account.LastBoundAt,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "user_id"},
				{Name: "channel_type"},
				{Name: "account_uid"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"display_name", "avatar_url", "credential_ciphertext",
				"credential_version", "last_bound_at", "updated_at",
			}),
		}).
		Create(&m).Error
	if err != nil {
		return domain.ChannelAccount{}, err
	}
	// Re-read to get the actual ID (in case of conflict/upsert)
	var result models.ChannelAccount
	err = r.db.WithContext(ctx).
		Where("user_id = ? AND channel_type = ? AND account_uid = ?", account.UserID, account.ChannelType, account.AccountUID).
		First(&result).Error
	if err != nil {
		return domain.ChannelAccount{}, err
	}
	return toDomainChannelAccount(result), nil
}

func (r *ChannelAccountRepository) GetByID(ctx context.Context, id string) (domain.ChannelAccount, error) {
	var m models.ChannelAccount
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.ChannelAccount{}, domain.ErrNotFound
		}
		return domain.ChannelAccount{}, err
	}
	return toDomainChannelAccount(m), nil
}

func (r *ChannelAccountRepository) ListByUserID(ctx context.Context, userID string, channelType string) ([]domain.ChannelAccount, error) {
	var ms []models.ChannelAccount
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if channelType != "" {
		q = q.Where("channel_type = ?", channelType)
	}
	if err := q.Find(&ms).Error; err != nil {
		return nil, err
	}
	result := make([]domain.ChannelAccount, len(ms))
	for i, m := range ms {
		result[i] = toDomainChannelAccount(m)
	}
	return result, nil
}

func toDomainChannelAccount(m models.ChannelAccount) domain.ChannelAccount {
	return domain.ChannelAccount{
		ID:                   m.ID,
		UserID:               m.UserID,
		ChannelType:          m.ChannelType,
		AccountUID:           m.AccountUID,
		DisplayName:          m.DisplayName,
		AvatarURL:            m.AvatarURL,
		CredentialCiphertext: m.CredentialCiphertext,
		CredentialVersion:    m.CredentialVersion,
		LastBoundAt:          m.LastBoundAt,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
}
