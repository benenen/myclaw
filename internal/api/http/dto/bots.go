package dto

type CreateBotRequest struct {
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	ChannelType string `json:"channel_type"`
}

type CreateBotResponse struct {
	BotID            string `json:"bot_id"`
	Name             string `json:"name"`
	ChannelType      string `json:"channel_type"`
	ConnectionStatus string `json:"connection_status"`
	ChannelAccountID string `json:"channel_account_id,omitempty"`
}

type BotResponse struct {
	BotID            string `json:"bot_id"`
	Name             string `json:"name"`
	ChannelType      string `json:"channel_type"`
	ConnectionStatus string `json:"connection_status"`
	ChannelAccountID string `json:"channel_account_id,omitempty"`
}

type ConnectBotRequest struct {
	BotID string `json:"bot_id"`
}

type DeleteBotRequest struct {
	BotID string `json:"bot_id"`
}

type ConnectBotResponse struct {
	BotID         string `json:"bot_id"`
	BindingID     string `json:"binding_id"`
	Status        string `json:"status"`
	QRCodePayload string `json:"qr_code_payload"`
	QRShareURL    string `json:"qr_share_url"`
	ExpiresAt     any    `json:"expires_at,omitempty"`
}

type RefreshBotLoginResponse struct {
	BotID            string `json:"bot_id"`
	BindingID        string `json:"binding_id"`
	Status           string `json:"status"`
	QRCodePayload    string `json:"qr_code_payload"`
	QRShareURL       string `json:"qr_share_url"`
	ExpiresAt        any    `json:"expires_at,omitempty"`
	ChannelAccountID string `json:"channel_account_id,omitempty"`
	ConnectionStatus string `json:"connection_status"`
}
