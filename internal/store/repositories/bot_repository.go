package repositories

import (
	"context"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
)

type BotRepository struct {
	db *gorm.DB
}

func NewBotRepository(db *gorm.DB) *BotRepository {
	return &BotRepository{db: db}
}

func (r *BotRepository) Create(ctx context.Context, bot domain.Bot) (domain.Bot, error) {
	now := time.Now().UTC()
	m := models.Bot{
		ID:                bot.ID,
		UserID:            bot.UserID,
		Name:              bot.Name,
		ChannelType:       bot.ChannelType,
		ChannelAccountID:  bot.ChannelAccountID,
		ConnectionStatus:  bot.ConnectionStatus,
		ConnectionError:   bot.ConnectionError,
		AgentCapabilityID: bot.AgentCapabilityID,
		AgentMode:         bot.AgentMode,
		LastConnectedAt:   bot.LastConnectedAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return domain.Bot{}, err
	}
	return toDomainBot(m), nil
}

func (r *BotRepository) GetByID(ctx context.Context, id string) (domain.Bot, error) {
	var m models.Bot
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return domain.Bot{}, domain.ErrNotFound
		}
		return domain.Bot{}, err
	}
	return toDomainBot(m), nil
}

func (r *BotRepository) ListByUserID(ctx context.Context, userID string) ([]domain.Bot, error) {
	var rows []models.Bot
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.Bot, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDomainBot(row))
	}
	return items, nil
}

func (r *BotRepository) ListWithAccounts(ctx context.Context) ([]domain.Bot, error) {
	var rows []models.Bot
	if err := r.db.WithContext(ctx).Where("channel_account_id <> ''").Order("created_at desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]domain.Bot, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDomainBot(row))
	}
	return items, nil
}

func (r *BotRepository) Update(ctx context.Context, bot domain.Bot) (domain.Bot, error) {
	m := models.Bot{
		ID:                bot.ID,
		UserID:            bot.UserID,
		Name:              bot.Name,
		ChannelType:       bot.ChannelType,
		ChannelAccountID:  bot.ChannelAccountID,
		ConnectionStatus:  bot.ConnectionStatus,
		ConnectionError:   bot.ConnectionError,
		AgentCapabilityID: bot.AgentCapabilityID,
		AgentMode:         bot.AgentMode,
		LastConnectedAt:   bot.LastConnectedAt,
		CreatedAt:         bot.CreatedAt,
		UpdatedAt:         time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Save(&m).Error; err != nil {
		return domain.Bot{}, err
	}
	return toDomainBot(m), nil
}

func (r *BotRepository) DeleteByID(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.Bot{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func toDomainBot(m models.Bot) domain.Bot {
	return domain.Bot{
		ID:                m.ID,
		UserID:            m.UserID,
		Name:              m.Name,
		ChannelType:       m.ChannelType,
		ChannelAccountID:  m.ChannelAccountID,
		ConnectionStatus:  m.ConnectionStatus,
		ConnectionError:   m.ConnectionError,
		AgentCapabilityID: m.AgentCapabilityID,
		AgentMode:         m.AgentMode,
		LastConnectedAt:   m.LastConnectedAt,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}
}
