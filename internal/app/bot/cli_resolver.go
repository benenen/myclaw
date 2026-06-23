package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if spec.WorkDir != "" {
		if err := writeSystemPromptDoc(spec.WorkDir, capability.Key, bot.SystemPrompt); err != nil {
			return agent.Spec{}, err
		}
	}
	if len(bot.AgentEnv) > 0 {
		spec.Env = bot.AgentEnv
		log.Printf("agent launch env: bot_id=%s %s", botID, formatEnvKV(bot.AgentEnv))
	}
	if r.sessions != nil {
		if stored, err := r.sessions.Get(ctx, botID, capability.Key); err == nil {
			spec.ResumeSessionID = stored.SessionID
		} else if !errors.Is(err, domain.ErrNotFound) {
			// non-fatal: log and continue with no resume
			log.Printf("cli session lookup failed: bot_id=%s cli=%s error=%v", botID, capability.Key, err)
		}
	}
	entries := r.collectMCPServers(ctx, botID)
	if capability.Key == "codex" {
		spec.Args = append(spec.Args, codexMCPArgs(entries)...)
	} else if cfg, ok := mcpConfigJSON(entries); ok {
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

// writeSystemPromptDoc materializes the bot's system prompt into the CLI's native
// instruction file in the workspace (claude → CLAUDE.md, else → AGENTS.md). myclaw
// fully owns the file: a non-empty prompt overwrites it; an empty prompt removes it.
func writeSystemPromptDoc(workDir, cliKey, prompt string) error {
	docFile := "AGENTS.md"
	if cliKey == "claude" {
		docFile = "CLAUDE.md"
	}
	path := filepath.Join(workDir, docFile)
	if strings.TrimSpace(prompt) != "" {
		return os.WriteFile(path, []byte(prompt), 0o644)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// formatEnvKV renders env as sorted "KEY=VALUE" pairs (logged at launch).
func formatEnvKV(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}
	return strings.Join(parts, " ")
}

type mcpEntry struct {
	Name, ServerType, URL, Command string
	Args                           []string
}

func (r *BotCLIResolver) collectMCPServers(ctx context.Context, botID string) []mcpEntry {
	var out []mcpEntry
	if r.mcpURL != "" {
		out = append(out, mcpEntry{Name: "myclaw", ServerType: mcpserver.TypeHTTP, URL: r.mcpURL})
	}
	if r.mcpServers != nil {
		servers, err := r.mcpServers.ListEnabledByBot(ctx, botID)
		if err != nil {
			log.Printf("mcp server list failed for bot %s: %v", botID, err)
		} else {
			for _, s := range servers {
				out = append(out, mcpEntry{Name: s.Name, ServerType: s.ServerType, URL: s.URL, Command: s.Command, Args: s.Args})
			}
		}
	}
	return out
}

// mcpConfigJSON renders the claude/opencode --mcp-config value (all types).
func mcpConfigJSON(entries []mcpEntry) (string, bool) {
	servers := map[string]any{}
	for _, e := range entries {
		cfg := map[string]any{"type": e.ServerType}
		switch e.ServerType {
		case mcpserver.TypeHTTP:
			cfg["url"] = e.URL
		case mcpserver.TypeStdio:
			args := e.Args
			if args == nil {
				args = []string{}
			}
			cfg["command"] = e.Command
			cfg["args"] = args
		default:
			continue
		}
		servers[e.Name] = cfg
	}
	if len(servers) == 0 {
		return "", false
	}
	data, _ := json.Marshal(map[string]any{"mcpServers": servers})
	return string(data), true
}

// codexMCPArgs renders `-c mcp_servers.*` overrides for STDIO servers (codex has
// no --mcp-config; http servers are skipped — codex injection is stdio-only here).
func codexMCPArgs(entries []mcpEntry) []string {
	var args []string
	for _, e := range entries {
		if e.ServerType != mcpserver.TypeStdio {
			if e.ServerType == mcpserver.TypeHTTP {
				log.Printf("codex: skipping http mcp server %q (stdio-only injection)", e.Name)
			}
			continue
		}
		argsJSON, _ := json.Marshal(e.Args)
		if e.Args == nil {
			argsJSON = []byte("[]")
		}
		args = append(args,
			"-c", fmt.Sprintf("mcp_servers.%s.command=%q", e.Name, e.Command),
			"-c", fmt.Sprintf("mcp_servers.%s.args=%s", e.Name, string(argsJSON)),
		)
	}
	return args
}

func (r *BotCLIResolver) timeoutForMode(mode string) time.Duration {
	return r.timeout
}
