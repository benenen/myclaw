package dto

type HttpChannelMessageRequest struct {
	BotID       string `json:"bot_id"`
	UserID      string `json:"user_id"`
	Text        string `json:"text"`
	MessageID   string `json:"message_id,omitempty"`
	CallbackURL string `json:"callback_url"`
}

type HttpChannelChatRequest struct {
	BotID string `json:"bot_id"`
	Text  string `json:"text"`
}

type HttpChannelChatResponse struct {
	BotID     string `json:"bot_id"`
	UserID    string `json:"user_id"`
	Text      string `json:"text"`
	MessageID string `json:"message_id"`
}
