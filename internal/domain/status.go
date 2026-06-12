package domain

const (
	BindingStatusPending   = "pending"
	BindingStatusQRReady   = "qr_ready"
	BindingStatusConfirmed = "confirmed"
	BindingStatusFailed    = "failed"
	BindingStatusExpired   = "expired"

	BotConnectionStatusLoginRequired = "login_required"
	BotConnectionStatusConnecting    = "connecting"
	BotConnectionStatusConnected     = "connected"
	BotConnectionStatusError         = "error"

	BotTypeChannel  = "channel"
	BotTypeHook     = "hook"
	BotTypeSubagent = "subagent"
)

// Bot roles. Orchestrator bots are the "brain"; empty means a normal bot.
const (
	BotRoleOrchestrator = "orchestrator"
)

// Sub-agent registry kinds and health values.
const (
	RegisteredAgentKindLocal  = "local"
	RegisteredAgentKindRemote = "remote"

	RegisteredAgentHealthy   = "healthy"
	RegisteredAgentUnhealthy = "unhealthy"
)
