package dto

type RegisterAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
	AuthToken   string `json:"auth_token,omitempty"`
}

type RegisterAgentResponse struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type HeartbeatRequest struct {
	Name string `json:"name"`
}
