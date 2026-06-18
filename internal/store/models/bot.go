package models

import "time"

type Bot struct {
	ID                string `gorm:"primaryKey"`
	UserID            string `gorm:"not null;index:idx_bots_user_id"`
	Name              string `gorm:"not null"`
	Type              string `gorm:"not null;default:''"`
	ChannelType       string `gorm:"not null"`
	ChannelAccountID  string `gorm:"not null;default:''"`
	ConnectionStatus  string `gorm:"not null"`
	ConnectionError   string `gorm:"not null;default:''"`
	AgentCapabilityID string `gorm:"not null;default:''"`
	AgentMode         string `gorm:"not null;default:''"`
	Role              string `gorm:"not null;default:''"`
	CLIAlias          string `gorm:"not null;default:''"`
	LastConnectedAt   *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
