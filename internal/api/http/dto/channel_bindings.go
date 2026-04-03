package dto

import "time"

type CreateBindingRequest struct {
	UserID      string `json:"user_id"`
	ChannelType string `json:"channel_type"`
}

type CreateBindingResponse struct {
	BindingID     string     `json:"binding_id"`
	Status        string     `json:"status"`
	QRCodePayload string     `json:"qr_code_payload"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

type BindingDetailResponse struct {
	BindingID        string     `json:"binding_id"`
	Status           string     `json:"status"`
	ChannelType      string     `json:"channel_type"`
	ChannelAccountID string     `json:"channel_account_id,omitempty"`
	DisplayName      string     `json:"display_name,omitempty"`
	AccountUID       string     `json:"account_uid,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
}
