package dto

import "time"

type ChannelAccountItem struct {
	ID              string     `json:"id"`
	ChannelType     string     `json:"channel_type"`
	AccountUID      string     `json:"account_uid"`
	DisplayName     string     `json:"display_name"`
	AvatarURL       string     `json:"avatar_url"`
	HasActiveAppKey bool       `json:"has_active_app_key"`
	LastBoundAt     *time.Time `json:"last_bound_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type CreateAppKeyRequest struct {
	ChannelAccountID string `json:"channel_account_id"`
}

type CreateAppKeyResponse struct {
	KeyID        string    `json:"key_id"`
	AppKey       string    `json:"app_key"`
	AppKeyPrefix string    `json:"app_key_prefix"`
	CreatedAt    time.Time `json:"created_at"`
}

type DisableAppKeyRequest struct {
	ChannelAccountID string `json:"channel_account_id,omitempty"`
	KeyID            string `json:"key_id,omitempty"`
}
