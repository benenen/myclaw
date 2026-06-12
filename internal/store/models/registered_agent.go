package models

import "time"

type RegisteredAgent struct {
	ID              string `gorm:"primaryKey"`
	Name            string `gorm:"not null;uniqueIndex"`
	Description     string `gorm:"not null;default:''"`
	Kind            string `gorm:"not null;default:'local'"`
	BotID           string `gorm:"not null;default:''"`
	Endpoint        string `gorm:"not null;default:''"`
	AuthToken       string `gorm:"not null;default:''"`
	Health          string `gorm:"not null;default:'healthy'"`
	LastHeartbeatAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (RegisteredAgent) TableName() string { return "registered_agents" }
