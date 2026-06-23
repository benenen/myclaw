package dto

type CreateBotRequest struct {
	UserID            string            `json:"user_id"`
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Role              string            `json:"role,omitempty"`
	ChannelType       string            `json:"channel_type"`
	AgentCapabilityID string            `json:"agent_capability_id,omitempty"`
	AgentMode         string            `json:"agent_mode,omitempty"`
	SystemPrompt      string            `json:"system_prompt,omitempty"`
	AgentEnv          map[string]string `json:"agent_env,omitempty"`
}

type CreateBotResponse struct {
	BotID             string            `json:"bot_id"`
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Role              string            `json:"role,omitempty"`
	ChannelType       string            `json:"channel_type"`
	ConnectionStatus  string            `json:"connection_status"`
	ChannelAccountID  string            `json:"channel_account_id,omitempty"`
	AgentCapabilityID string            `json:"agent_capability_id,omitempty"`
	AgentMode         string            `json:"agent_mode,omitempty"`
	SystemPrompt      string            `json:"system_prompt,omitempty"`
	AgentEnv          map[string]string `json:"agent_env,omitempty"`
}

type BotResponse struct {
	BotID             string            `json:"bot_id"`
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Role              string            `json:"role,omitempty"`
	ChannelType       string            `json:"channel_type"`
	ConnectionStatus  string            `json:"connection_status"`
	ChannelAccountID  string            `json:"channel_account_id,omitempty"`
	AgentCapabilityID string            `json:"agent_capability_id,omitempty"`
	AgentMode         string            `json:"agent_mode,omitempty"`
	CLIAlias          string            `json:"cli_alias,omitempty"`
	MCPServerIDs      []string          `json:"mcp_server_ids"`
	SystemPrompt      string            `json:"system_prompt,omitempty"`
	AgentEnv          map[string]string `json:"agent_env,omitempty"`
}

type ConfigureBotAgentRequest struct {
	BotID             string            `json:"bot_id"`
	AgentCapabilityID string            `json:"agent_capability_id"`
	AgentMode         string            `json:"agent_mode"`
	CLIAlias          string            `json:"cli_alias,omitempty"`
	MCPServerIDs      []string          `json:"mcp_server_ids,omitempty"`
	SystemPrompt      string            `json:"system_prompt,omitempty"`
	AgentEnv          map[string]string `json:"agent_env,omitempty"`
}

type ConfigureBotAgentResponse = BotResponse

type MCPServerResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ServerType string `json:"server_type"`
	Enabled    bool   `json:"enabled"`
}

type ConnectBotRequest struct {
	BotID     string `json:"bot_id"`
	AppID     string `json:"app_id,omitempty"`
	AppSecret string `json:"app_secret,omitempty"`
}

type DeleteBotRequest struct {
	BotID string `json:"bot_id"`
}

type SimulateBotMessageRequest struct {
	BotID       string `json:"bot_id"`
	From        string `json:"from"`
	Text        string `json:"text"`
	MessageID   string `json:"message_id,omitempty"`
	RecipientID string `json:"recipient_id,omitempty"`
}

type SimulateBotMessageResponse struct {
	BotID       string `json:"bot_id"`
	From        string `json:"from"`
	Text        string `json:"text"`
	MessageID   string `json:"message_id"`
	RecipientID string `json:"recipient_id"`
}

type ConnectBotResponse struct {
	BotID             string `json:"bot_id"`
	BindingID         string `json:"binding_id"`
	Status            string `json:"status"`
	QRCodePayload     string `json:"qr_code_payload"`
	QRShareURL        string `json:"qr_share_url"`
	ExpiresAt         any    `json:"expires_at,omitempty"`
	ConnectionStatus  string `json:"connection_status,omitempty"`
	ChannelAccountID  string `json:"channel_account_id,omitempty"`
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
