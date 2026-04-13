package dto

type AgentCapabilityResponse struct {
	ID             string   `json:"id"`
	Key            string   `json:"key"`
	Label          string   `json:"label"`
	SupportedModes []string `json:"supported_modes"`
}
