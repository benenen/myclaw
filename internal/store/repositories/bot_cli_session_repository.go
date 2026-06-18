package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BotCLISessionRepository struct {
	db *gorm.DB
}

func NewBotCLISessionRepository(db *gorm.DB) *BotCLISessionRepository {
	return &BotCLISessionRepository{db: db}
}

func (r *BotCLISessionRepository) Upsert(ctx context.Context, s domain.BotCLISession) error {
	m := models.BotCLISession{
		BotID:     s.BotID,
		CLIType:   s.CLIType,
		SessionID: s.SessionID,
		WorkDir:   s.WorkDir,
		UpdatedAt: time.Now().UTC(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "bot_id"}, {Name: "cli_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"session_id", "work_dir", "updated_at"}),
	}).Create(&m).Error
}

func (r *BotCLISessionRepository) Get(ctx context.Context, botID, cliType string) (domain.BotCLISession, error) {
	var m models.BotCLISession
	if err := r.db.WithContext(ctx).Where("bot_id = ? AND cli_type = ?", botID, cliType).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.BotCLISession{}, domain.ErrNotFound
		}
		return domain.BotCLISession{}, err
	}
	return domain.BotCLISession{
		BotID: m.BotID, CLIType: m.CLIType, SessionID: m.SessionID, WorkDir: m.WorkDir, UpdatedAt: m.UpdatedAt,
	}, nil
}

var _ domain.BotCLISessionRepository = (*BotCLISessionRepository)(nil)
