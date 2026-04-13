package dto

type CreateBotRequest struct {
	UserID            string `json:"user_id"`
	Name              string `json:"name"`
	ChannelType       string `json:"channel_type"`
	AgentCapabilityID string `json:"agent_capability_id,omitempty"`
	AgentMode         string `json:"agent_mode,omitempty"`
}

type CreateBotResponse struct {
	BotID             string `json:"bot_id"`
	Name              string `json:"name"`
	ChannelType       string `json:"channel_type"`
	ConnectionStatus  string `json:"connection_status"`
	ChannelAccountID  string `json:"channel_account_id,omitempty"`
	AgentCapabilityID string `json:"agent_capability_id,omitempty"`
	AgentMode         string `json:"agent_mode,omitempty"`
}

type BotResponse struct {
	BotID             string `json:"bot_id"`
	Name              string `json:"name"`
	ChannelType       string `json:"channel_type"`
	ConnectionStatus  string `json:"connection_status"`
	ChannelAccountID  string `json:"channel_account_id,omitempty"`
	AgentCapabilityID string `json:"agent_capability_id,omitempty"`
	AgentMode         string `json:"agent_mode,omitempty"`
}

type ConfigureBotAgentRequest struct {
	BotID             string `json:"bot_id"`
	AgentCapabilityID string `json:"agent_capability_id"`
	AgentMode         string `json:"agent_mode"`
}

type ConfigureBotAgentResponse = BotResponse

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
