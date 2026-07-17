package bot

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

// The myclaw MCP entry must carry the bot's identity so the scheduled-task
// tools (schedule_task etc.) know which session they act on.
func TestResolveInjectsPerBotMCPURL(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_worker",
		Name:              "worker-bot",
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_claude": {
			ID:             "cap_claude",
			Command:        "/usr/local/bin/claude",
			Args:           []string{"--stream-json"},
			SupportedModes: []string{"session"},
			Available:      true,
		},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{
		Timeout: time.Minute,
		MCPURL:  "http://127.0.0.1:8080/mcp",
	})

	spec, err := r.Resolve(context.Background(), "bot_worker")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	idx := slices.Index(spec.Args, "--mcp-config")
	if idx < 0 || idx+1 >= len(spec.Args) {
		t.Fatalf("--mcp-config flag missing in args: %v", spec.Args)
	}
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(spec.Args[idx+1]), &cfg); err != nil {
		t.Fatalf("mcp-config not valid JSON: %v", err)
	}
	myclaw, ok := cfg.MCPServers["myclaw"]
	if !ok {
		t.Fatalf("myclaw missing: %v", cfg.MCPServers)
	}
	if got := myclaw["url"]; got != "http://127.0.0.1:8080/mcp?bot_id=bot_worker" {
		t.Fatalf("myclaw url = %v, want per-bot url with bot_id", got)
	}
}
