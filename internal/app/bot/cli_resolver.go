package bot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

var (
	ErrBotCLIConfigMissing   = errors.New("bot cli config missing")
	ErrBotCLIUnavailable     = errors.New("bot cli unavailable")
	ErrBotCLIUnsupportedMode = errors.New("bot cli mode unsupported")
)

type BotCLIResolverConfig struct {
	Timeout             time.Duration
	WorkspaceRoot       string
	SQLitePath          string
	OrchestratorTimeout time.Duration
	MCPURL              string
	OrchestratorPrompt  string
}

type BotCLIResolver struct {
	bots                domain.BotRepository
	capabilities        domain.AgentCapabilityRepository
	timeout             time.Duration
	workspaceRoot       string
	sqlitePath          string
	orchestratorTimeout time.Duration
	mcpURL              string
	orchestratorPrompt  string
}

func NewBotCLIResolver(bots domain.BotRepository, capabilities domain.AgentCapabilityRepository, cfg BotCLIResolverConfig) *BotCLIResolver {
	return &BotCLIResolver{
		bots:                bots,
		capabilities:        capabilities,
		timeout:             cfg.Timeout,
		workspaceRoot:       cfg.WorkspaceRoot,
		sqlitePath:          cfg.SQLitePath,
		orchestratorTimeout: cfg.OrchestratorTimeout,
		mcpURL:              cfg.MCPURL,
		orchestratorPrompt:  cfg.OrchestratorPrompt,
	}
}

func (r *BotCLIResolver) Resolve(ctx context.Context, botID string) (agent.Spec, error) {
	bot, err := r.bots.GetByID(ctx, botID)
	if err != nil {
		return agent.Spec{}, err
	}
	if bot.AgentCapabilityID == "" || bot.AgentMode == "" {
		return agent.Spec{}, ErrBotCLIConfigMissing
	}
	capability, err := r.capabilities.GetByID(ctx, bot.AgentCapabilityID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return agent.Spec{}, ErrBotCLIConfigMissing
		}
		return agent.Spec{}, err
	}
	alias := strings.TrimSpace(bot.CLIAlias)
	if !slices.Contains(capability.SupportedModes, bot.AgentMode) {
		return agent.Spec{}, ErrBotCLIUnsupportedMode
	}
	if alias == "" {
		if !capability.Available {
			return agent.Spec{}, ErrBotCLIUnavailable
		}
		if capability.Command == "" {
			return agent.Spec{}, ErrBotCLIConfigMissing
		}
	}
	command := capability.Command
	if alias != "" {
		command = alias
	}
	spec := agent.Spec{
		BotID:      botID,
		BotName:    bot.Name,
		Type:       bot.AgentMode,
		Command:    command,
		Args:       append([]string(nil), capability.Args...),
		Timeout:    r.timeoutForMode(bot.AgentMode),
		SQLitePath: r.sqlitePath,
		RealCLI:    alias != "",
	}
	if r.workspaceRoot != "" {
		spec.WorkDir = filepath.Join(r.workspaceRoot, botID, "workspace")
		if err := os.MkdirAll(spec.WorkDir, 0o755); err != nil {
			return agent.Spec{}, err
		}
	}
	if bot.Role == domain.BotRoleOrchestrator {
		spec.Orchestrator = true
		if r.orchestratorTimeout > 0 {
			spec.Timeout = r.orchestratorTimeout
		}
		extra := []string{}
		if r.mcpURL != "" {
			extra = append(extra, "--mcp-config", mcpConfigJSON(r.mcpURL))
		}
		if r.orchestratorPrompt != "" {
			extra = append(extra, "--append-system-prompt", r.orchestratorPrompt)
		}
		spec.Args = append(spec.Args, extra...)
	}
	return spec, nil
}

func mcpConfigJSON(url string) string {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"myclaw": map[string]any{
				"type": "http",
				"url":  url,
			},
		},
	}
	data, _ := json.Marshal(cfg)
	return string(data)
}

func (r *BotCLIResolver) timeoutForMode(mode string) time.Duration {
	return r.timeout
}
