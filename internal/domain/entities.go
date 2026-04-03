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

type AppKey struct {
	ID               string
	UserID           string
	ChannelAccountID string
	AppKeyHash       string
	AppKeyPrefix     string
	Status           string
	LastUsedAt       *time.Time
	CreatedAt        time.Time
	DisabledAt       *time.Time
}
