package domain

import (
	"context"
	"time"
)

type User struct {
	ID             string
	ExternalUserID string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ChannelAccount struct {
	ID                   string
	UserID               string
	ChannelType          string
	AccountUID           string
	DisplayName          string
	AvatarURL            string
	CredentialCiphertext []byte
	CredentialVersion    int
	LastBoundAt          *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ChannelBinding struct {
	ID                 string
	BotID              string
	UserID             string
	ChannelType        string
	Status             string
	ProviderBindingRef string
	QRCodePayload      string
	ExpiresAt          *time.Time
	ErrorMessage       string
	ChannelAccountID   string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	FinishedAt         *time.Time
}

type AgentCapability struct {
	ID              string
	Key             string
	Label           string
	Command         string
	Args            []string
	SupportedModes  []string
	Available       bool
	DetectionSource string
	LastDetectedAt  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Bot struct {
	ID                string
	UserID            string
	Name              string
	Type              string
	ChannelType       string
	ChannelAccountID  string
	ConnectionStatus  string
	ConnectionError   string
	AgentCapabilityID string
	AgentMode         string
	Role              string
	CLIAlias          string
	Workspace         string
	LastConnectedAt   *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type BotCLISession struct {
	BotID     string
	CLIType   string
	SessionID string
	WorkDir   string
	UpdatedAt time.Time
}

type BotCLISessionRepository interface {
	Upsert(ctx context.Context, s BotCLISession) error
	Get(ctx context.Context, botID, cliType string) (BotCLISession, error)
}

type RegisteredAgent struct {
	ID            string
	Name          string
	Description   string
	Kind          string
	BotID         string
	Endpoint      string
	AuthToken     string
	Health        string
	LastHeartbeat *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
