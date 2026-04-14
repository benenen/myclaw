package dto

type AgentCapabilityResponse struct {
	ID              string   `json:"id"`
	Key             string   `json:"key"`
	Label           string   `json:"label"`
	Command         string   `json:"command"`
	Available       bool     `json:"available"`
	DetectionSource string   `json:"detection_source,omitempty"`
	SupportedModes  []string `json:"supported_modes"`
}
