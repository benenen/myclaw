package bot

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/app/mcpserver"
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
	sessions            domain.BotCLISessionRepository
	timeout             time.Duration
	workspaceRoot       string
	sqlitePath          string
	orchestratorTimeout time.Duration
	mcpURL              string
	orchestratorPrompt  string
	mcpServers          domain.MCPServerRepository
}

func NewBotCLIResolver(bots domain.BotRepository, capabilities domain.AgentCapabilityRepository, sessions domain.BotCLISessionRepository, cfg BotCLIResolverConfig) *BotCLIResolver {
	return &BotCLIResolver{
		bots:                bots,
		capabilities:        capabilities,
		sessions:            sessions,
		timeout:             cfg.Timeout,
		workspaceRoot:       cfg.WorkspaceRoot,
		sqlitePath:          cfg.SQLitePath,
		orchestratorTimeout: cfg.OrchestratorTimeout,
		mcpURL:              cfg.MCPURL,
		orchestratorPrompt:  cfg.OrchestratorPrompt,
	}
}

func (r *BotCLIResolver) SetMCPServerRepository(repo domain.MCPServerRepository) {
	r.mcpServers = repo
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
	workDir := strings.TrimSpace(bot.Workspace)
	if workDir == "" && r.workspaceRoot != "" {
		workDir = filepath.Join(r.workspaceRoot, botID, "workspace")
	}
	if workDir != "" {
		spec.WorkDir = workDir
		if err := os.MkdirAll(spec.WorkDir, 0o755); err != nil {
			return agent.Spec{}, err
		}
	}
	if r.sessions != nil {
		if stored, err := r.sessions.Get(ctx, botID, capability.Key); err == nil {
			spec.ResumeSessionID = stored.SessionID
		} else if !errors.Is(err, domain.ErrNotFound) {
			// non-fatal: log and continue with no resume
			log.Printf("cli session lookup failed: bot_id=%s cli=%s error=%v", botID, capability.Key, err)
		}
	}
	if cfg, ok := r.buildMCPConfigJSON(ctx, botID); ok {
		spec.Args = append(spec.Args, "--mcp-config", cfg)
	}
	if bot.Role == domain.BotRoleOrchestrator {
		spec.Orchestrator = true
		if r.orchestratorTimeout > 0 {
			spec.Timeout = r.orchestratorTimeout
		}
		if r.orchestratorPrompt != "" {
			spec.Args = append(spec.Args, "--append-system-prompt", r.orchestratorPrompt)
		}
	}
	return spec, nil
}

func (r *BotCLIResolver) buildMCPConfigJSON(ctx context.Context, botID string) (string, bool) {
	mcpServers := map[string]any{}
	if r.mcpURL != "" {
		mcpServers["myclaw"] = map[string]any{"type": "http", "url": r.mcpURL}
	}
	if r.mcpServers != nil {
		servers, err := r.mcpServers.ListEnabledByBot(ctx, botID)
		if err != nil {
			log.Printf("mcp server list failed for bot %s: %v", botID, err)
		} else {
			for _, srv := range servers {
				cfg := map[string]any{"type": srv.ServerType}
				switch srv.ServerType {
				case mcpserver.TypeHTTP:
					cfg["url"] = srv.URL
				case mcpserver.TypeStdio:
					args := srv.Args
					if args == nil {
						args = []string{}
					}
					cfg["command"] = srv.Command
					cfg["args"] = args
				default:
					log.Printf("unknown mcp server type %q for %q, skipping", srv.ServerType, srv.Name)
					continue
				}
				mcpServers[srv.Name] = cfg
			}
		}
	}
	if len(mcpServers) == 0 {
		return "", false
	}
	data, _ := json.Marshal(map[string]any{"mcpServers": mcpServers})
	return string(data), true
}

func (r *BotCLIResolver) timeoutForMode(mode string) time.Duration {
	return r.timeout
}
