package models

import "time"

type AgentCapability struct {
	ID                 string `gorm:"primaryKey"`
	Key                string `gorm:"not null;uniqueIndex"`
	Label              string `gorm:"not null"`
	Command            string `gorm:"not null"`
	ArgsJSON           string `gorm:"not null;default:'[]'"`
	SupportedModesJSON string `gorm:"not null;default:'[]'"`
	Available          bool   `gorm:"not null;default:false"`
	DetectionSource    string `gorm:"not null;default:''"`
	LastDetectedAt     *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
