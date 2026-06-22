package models

import "time"

type MCPServer struct {
	ID         string `gorm:"primaryKey"`
	Name       string `gorm:"not null;uniqueIndex"`
	ServerType string `gorm:"column:server_type;not null;default:'http'"`
	URL        string `gorm:"column:url;not null;default:''"`
	Command    string `gorm:"column:command;not null;default:''"`
	ArgsJSON   string `gorm:"column:args_json;not null;default:'[]'"`
	Enabled    *bool  `gorm:"not null;default:true"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (MCPServer) TableName() string { return "mcp_servers" }

type BotMCPServer struct {
	BotID       string `gorm:"column:bot_id;primaryKey"`
	MCPServerID string `gorm:"column:mcp_server_id;primaryKey"`
	CreatedAt   time.Time
}

func (BotMCPServer) TableName() string { return "bot_mcp_servers" }
