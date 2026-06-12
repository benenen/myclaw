package orchestration

import _ "embed"

//go:embed prompt.md
var orchestratorPrompt string

// OrchestratorPrompt returns the system prompt injected into brain sessions.
func OrchestratorPrompt() string { return orchestratorPrompt }
