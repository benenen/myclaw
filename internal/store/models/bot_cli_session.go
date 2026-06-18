package models

import "time"

type BotCLISession struct {
	BotID     string `gorm:"primaryKey;column:bot_id"`
	CLIType   string `gorm:"primaryKey;column:cli_type"`
	SessionID string `gorm:"not null;default:'';column:session_id"`
	WorkDir   string `gorm:"not null;default:'';column:work_dir"`
	UpdatedAt time.Time
}

func (BotCLISession) TableName() string { return "bot_cli_sessions" }
