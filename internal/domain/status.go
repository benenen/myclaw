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

	BotTypeChannel = "channel"
	BotTypeHook    = "hook"
)

// Bot roles. Orchestrator bots are the "brain"; empty means a normal bot.
const (
	BotRoleOrchestrator = "orchestrator"
)

// Sub-agent types (Bot.Type) and registry kinds.
const (
	BotTypeSubagent = "subagent"

	RegisteredAgentKindLocal  = "local"
	RegisteredAgentKindRemote = "remote"

	RegisteredAgentHealthy   = "healthy"
	RegisteredAgentUnhealthy = "unhealthy"
)
