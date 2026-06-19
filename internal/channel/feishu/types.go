package feishu

import "context"

// ChannelType is the registered channel-type string for Feishu.
const ChannelType = "feishu"

// Credentials are the per-bot Feishu self-built app credentials plus the
// bot's own open_id (used to detect @mentions in group chats). This is the
// shape persisted (encrypted) in the channel account credential payload.
type Credentials struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	BotOpenID string `json:"bot_open_id"`
}

// AppInfo is returned when validating credentials against the Feishu API.
type AppInfo struct {
	AppName   string
	BotOpenID string
}

// InboundMessage is a normalized inbound Feishu message.
type InboundMessage struct {
	MessageID        string
	ChatID           string
	ChatType         string // "p2p" | "group"
	SenderOpenID     string
	Text             string
	MentionedOpenIDs []string
}

// SendParams describes an outbound text message. A non-empty ReplyMessageID
// makes it a reply threaded under the original message. Mentions holds
// open_ids that should be @-mentioned in the message (group replies only).
type SendParams struct {
	ChatID         string
	Text           string
	ReplyMessageID string
	Mentions       []string
}

// CardParams describes an outbound interactive-card message. A non-empty
// ReplyMessageID threads it under the original message.
type CardParams struct {
	ChatID         string
	Content        string // interactive-card JSON
	ReplyMessageID string
}

// feishuAPI abstracts the Feishu REST surface so provider/reply logic can be
// tested without real network calls. The real implementation lives in api.go.
type feishuAPI interface {
	ValidateApp(ctx context.Context, appID, appSecret string) (AppInfo, error)
	SendText(ctx context.Context, creds Credentials, p SendParams) error
	// CreateCard sends a new interactive card and returns its message id.
	CreateCard(ctx context.Context, creds Credentials, p CardParams) (string, error)
	// PatchCard replaces the content of an existing interactive card.
	PatchCard(ctx context.Context, creds Credentials, messageID, cardJSON string) error
}

// dialer opens a Feishu WebSocket long-connection. The real implementation
// lives in dialer.go.
type dialer interface {
	Dial(creds Credentials, onMessage func(InboundMessage)) (conn, error)
}

// conn is one live long-connection. Start blocks until ctx is cancelled or a
// fatal error occurs; cancelling ctx is how the caller stops it.
type conn interface {
	Start(ctx context.Context) error
}
