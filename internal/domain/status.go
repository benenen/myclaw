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
