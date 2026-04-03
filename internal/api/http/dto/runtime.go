package dto

type RuntimeConfigResponse struct {
	ChannelType      string         `json:"channel_type"`
	ChannelAccountID string         `json:"channel_account_id"`
	AccountUID       string         `json:"account_uid"`
	CredentialBlob   map[string]any `json:"credential_blob,omitempty"`
	RuntimeOptions   map[string]any `json:"runtime_options,omitempty"`
}
