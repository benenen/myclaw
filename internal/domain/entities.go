package domain

import "time"

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
	ChannelType       string
	ChannelAccountID  string
	ConnectionStatus  string
	ConnectionError   string
	AgentCapabilityID string
	AgentMode         string
	LastConnectedAt   *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
