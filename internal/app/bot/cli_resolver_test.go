package bot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

func TestResolveOrchestratorInjectsMCPAndPrompt(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_brain",
		Name:              "brain-bot",
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
		Role:              domain.BotRoleOrchestrator,
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
		Timeout:             time.Minute,
		OrchestratorTimeout: 25 * time.Minute,
		MCPURL:              "http://127.0.0.1:8080/mcp",
		OrchestratorPrompt:  "BRAIN-PROMPT",
	})

	spec, err := r.Resolve(context.Background(), "bot_brain")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !spec.Orchestrator {
		t.Fatal("expected Orchestrator=true")
	}
	if spec.Timeout != 25*time.Minute {
		t.Fatalf("expected orchestrator timeout, got %v", spec.Timeout)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--mcp-config") || !strings.Contains(joined, "/mcp") {
		t.Fatalf("expected mcp config in args: %v", spec.Args)
	}
	if !strings.Contains(joined, "--append-system-prompt") || !strings.Contains(joined, "BRAIN-PROMPT") {
		t.Fatalf("expected system prompt in args: %v", spec.Args)
	}
}

func TestBotCLIResolverResolveReturnsConfigForConfiguredAvailableCapability(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_1",
		Name:              "helper-bot",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {
			ID:             "cap_codex",
			Command:        "/usr/local/bin/codex",
			Args:           []string{"reply", "--plain"},
			SupportedModes: []string{"codex-exec", "session"},
			Available:      true,
		},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{Timeout: 45 * time.Second})

	spec, err := resolver.Resolve(context.Background(), "bot_1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Type != "codex-exec" {
		t.Fatalf("unexpected type: %q", spec.Type)
	}
	if spec.Command != "/usr/local/bin/codex" {
		t.Fatalf("unexpected command: %q", spec.Command)
	}
	if !slices.Equal(spec.Args, []string{"reply", "--plain"}) {
		t.Fatalf("unexpected args: %#v", spec.Args)
	}
	if spec.Timeout != 45*time.Second {
		t.Fatalf("unexpected timeout: %s", spec.Timeout)
	}
	if spec.BotID != "bot_1" {
		t.Fatalf("unexpected bot id: %q", spec.BotID)
	}
	if spec.BotName != "helper-bot" {
		t.Fatalf("unexpected bot name: %q", spec.BotName)
	}
	if spec.WorkDir != "" {
		t.Fatalf("unexpected workdir: %q", spec.WorkDir)
	}

	spec.Args[0] = "mutated"
	if capabilities.byID["cap_codex"].Args[0] != "reply" {
		t.Fatal("expected resolver to copy args")
	}
}

func TestBotCLIResolverResolveAssignsAndCreatesBotWorkspace(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "bots")
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_1",
		Name:              "helper-bot",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {
			ID:             "cap_codex",
			Command:        "/usr/local/bin/codex",
			SupportedModes: []string{"codex-exec"},
			Available:      true,
		},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{
		Timeout:       45 * time.Second,
		WorkspaceRoot: workspaceRoot,
	})

	spec, err := resolver.Resolve(context.Background(), "bot_1")
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(workspaceRoot, "bot_1", "workspace")
	if spec.WorkDir != want {
		t.Fatalf("WorkDir = %q, want %q", spec.WorkDir, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", want, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", want)
	}
}

func TestBotCLIResolverResolveAssignsSQLitePath(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_1",
		Name:              "helper-bot",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {
			ID:             "cap_codex",
			Command:        "/usr/local/bin/codex",
			SupportedModes: []string{"codex-exec"},
			Available:      true,
		},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{
		Timeout:    45 * time.Second,
		SQLitePath: "/tmp/myclaw/myclaw.db",
	})

	spec, err := resolver.Resolve(context.Background(), "bot_1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.SQLitePath != "/tmp/myclaw/myclaw.db" {
		t.Fatalf("SQLitePath = %q", spec.SQLitePath)
	}
}

func TestBotCLIResolverResolveUsesDedicatedCodexExecTimeout(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_1",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "codex-exec",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {
			ID:             "cap_codex",
			Command:        "/usr/local/bin/codex",
			SupportedModes: []string{"codex-exec"},
			Available:      true,
		},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{
		Timeout: 45 * time.Second,
	})

	spec, err := resolver.Resolve(context.Background(), "bot_1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Timeout != 45*time.Second {
		t.Fatalf("unexpected timeout: %s", spec.Timeout)
	}
}

func TestBotCLIResolverResolveReturnsConfigMissingWhenCapabilityMissing(t *testing.T) {
	resolver := NewBotCLIResolver(newBotRepoStub(domain.Bot{ID: "bot_1"}), &agentCapabilityRepoStub{}, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if !errors.Is(err, ErrBotCLIConfigMissing) {
		t.Fatalf("expected ErrBotCLIConfigMissing, got %v", err)
	}
}

func TestBotCLIResolverResolveReturnsUnavailableWhenCapabilityUnavailable(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_claude", AgentMode: "codex-exec"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_claude": {ID: "cap_claude", Available: false, SupportedModes: []string{"codex-exec"}},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if !errors.Is(err, ErrBotCLIUnavailable) {
		t.Fatalf("expected ErrBotCLIUnavailable, got %v", err)
	}
}

func TestBotCLIResolverResolveReturnsUnsupportedModeWhenCapabilityDoesNotSupportBotMode(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_claude", AgentMode: "session"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_claude": {ID: "cap_claude", Available: true, SupportedModes: []string{"codex-exec"}},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if !errors.Is(err, ErrBotCLIUnsupportedMode) {
		t.Fatalf("expected ErrBotCLIUnsupportedMode, got %v", err)
	}
}

func TestBotCLIResolverResolveReturnsCapabilityLookupError(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_codex", AgentMode: "codex-exec"})
	capabilities := &agentCapabilityRepoStub{getByIDErr: errors.New("lookup failed")}
	resolver := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if err == nil || err.Error() != "lookup failed" {
		t.Fatalf("expected lookup failed, got %v", err)
	}
}

func TestResolveAliasOverridesCommandAndBypassesAvailability(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_alias", Name: "b", AgentCapabilityID: "cap_codex",
		AgentMode: "acp", CLIAlias: "cx",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		// NOT available and canonical command — alias must bypass the gate.
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"acp"}, Available: false},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	spec, err := r.Resolve(context.Background(), "bot_alias")
	if err != nil {
		t.Fatalf("Resolve with alias should bypass availability: %v", err)
	}
	if spec.Command != "cx" {
		t.Fatalf("spec.Command = %q, want cx", spec.Command)
	}
	if !spec.RealCLI {
		t.Fatalf("spec.RealCLI = false, want true when alias set")
	}
}

func TestResolveNoAliasKeepsDefaultAndUnavailableErrors(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_noalias", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "acp",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"acp"}, Available: false},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	if _, err := r.Resolve(context.Background(), "bot_noalias"); !errors.Is(err, ErrBotCLIUnavailable) {
		t.Fatalf("no alias + unavailable should error ErrBotCLIUnavailable, got %v", err)
	}
}

func TestResolveAliasStillValidatesMode(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_alias_badmode", Name: "b", AgentCapabilityID: "cap_codex",
		AgentMode: "nope", CLIAlias: "cx",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"acp"}, Available: false},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})

	if _, err := r.Resolve(context.Background(), "bot_alias_badmode"); !errors.Is(err, ErrBotCLIUnsupportedMode) {
		t.Fatalf("alias set but unsupported mode should return ErrBotCLIUnsupportedMode, got %v", err)
	}
}

func TestResolveUsesBotWorkspaceWhenSet(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID: "bot_w", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp",
		Workspace: "/custom/ws",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_w")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.WorkDir != "/custom/ws" {
		t.Fatalf("WorkDir = %q, want /custom/ws", spec.WorkDir)
	}
}

type stubMCPServerRepo struct {
	domain.MCPServerRepository // embed; only ListEnabledByBot is exercised
	byBot map[string][]domain.MCPServer
}

func (s stubMCPServerRepo) ListEnabledByBot(_ context.Context, botID string) ([]domain.MCPServer, error) {
	return s.byBot[botID], nil
}

func TestResolveInjectsAttachedEnabledMCPServers(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_brain",
		Name:              "brain-bot",
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
		Role:              domain.BotRoleOrchestrator,
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
		Timeout:             time.Minute,
		OrchestratorTimeout: 25 * time.Minute,
		MCPURL:              "http://127.0.0.1:8080/mcp",
		OrchestratorPrompt:  "BRAIN-PROMPT",
	})
	r.SetMCPServerRepository(stubMCPServerRepo{byBot: map[string][]domain.MCPServer{
		"bot_brain": {
			{ID: "mcp_a", Name: "extra", ServerType: "http", URL: "http://extra", Enabled: true},
			{ID: "mcp_b", Name: "fs", ServerType: "stdio", Command: "npx", Args: []string{"-y", "srv"}, Enabled: true},
		},
	}})

	spec, err := r.Resolve(context.Background(), "bot_brain")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	idx := slices.Index(spec.Args, "--mcp-config")
	if idx < 0 || idx+1 >= len(spec.Args) {
		t.Fatalf("--mcp-config flag missing in %v", spec.Args)
	}
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(spec.Args[idx+1]), &cfg); err != nil {
		t.Fatalf("mcp-config not valid JSON: %v", err)
	}
	if _, ok := cfg.MCPServers["myclaw"]; !ok {
		t.Fatalf("myclaw missing: %v", cfg.MCPServers)
	}
	extra, ok := cfg.MCPServers["extra"]
	if !ok || extra["type"] != "http" || extra["url"] != "http://extra" {
		t.Fatalf("extra http server wrong: %+v", extra)
	}
	fs, ok := cfg.MCPServers["fs"]
	if !ok || fs["type"] != "stdio" || fs["command"] != "npx" {
		t.Fatalf("fs stdio server wrong: %+v", fs)
	}
}

func TestResolveInjectsMCPForNonOrchestratorBot(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_worker",
		Name:              "worker-bot",
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
		// Role left empty — not orchestrator
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
		// no MCPURL, no OrchestratorPrompt
	})
	r.SetMCPServerRepository(stubMCPServerRepo{byBot: map[string][]domain.MCPServer{
		"bot_worker": {
			{ID: "mcp_x", Name: "toolsrv", ServerType: "http", URL: "http://toolsrv", Enabled: true},
		},
	}})

	spec, err := r.Resolve(context.Background(), "bot_worker")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.Orchestrator {
		t.Fatal("expected Orchestrator=false for non-orchestrator bot")
	}

	joined := strings.Join(spec.Args, " ")
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
	srv, ok := cfg.MCPServers["toolsrv"]
	if !ok || srv["type"] != "http" || srv["url"] != "http://toolsrv" {
		t.Fatalf("expected toolsrv in mcp config: %v", cfg.MCPServers)
	}
	if strings.Contains(joined, "--append-system-prompt") {
		t.Fatalf("non-orchestrator must NOT get --append-system-prompt: %v", spec.Args)
	}
}

func TestResolveNoMCPConfigWhenNoServers(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_plain",
		Name:              "plain-bot",
		AgentCapabilityID: "cap_claude",
		AgentMode:         "session",
		// Role left empty — not orchestrator
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
	// no MCPURL and ListEnabledByBot returns empty slice
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{
		Timeout: time.Minute,
	})
	r.SetMCPServerRepository(stubMCPServerRepo{byBot: map[string][]domain.MCPServer{}})

	spec, err := r.Resolve(context.Background(), "bot_plain")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, arg := range spec.Args {
		if arg == "--mcp-config" {
			t.Fatalf("--mcp-config should NOT appear when no servers: %v", spec.Args)
		}
	}
}

type agentSessionRepoStub struct {
	byKey map[string]domain.BotCLISession // key = botID + "|" + cliType
}

func (s *agentSessionRepoStub) Upsert(_ context.Context, sess domain.BotCLISession) error {
	if s.byKey == nil {
		s.byKey = map[string]domain.BotCLISession{}
	}
	s.byKey[sess.BotID+"|"+sess.CLIType] = sess
	return nil
}
func (s *agentSessionRepoStub) Get(_ context.Context, botID, cliType string) (domain.BotCLISession, error) {
	if v, ok := s.byKey[botID+"|"+cliType]; ok {
		return v, nil
	}
	return domain.BotCLISession{}, domain.ErrNotFound
}

func TestResolveSetsResumeSessionFromStore(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_s", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	sessions := &agentSessionRepoStub{byKey: map[string]domain.BotCLISession{
		"bot_s|codex": {BotID: "bot_s", CLIType: "codex", SessionID: "conv_42"},
	}}
	r := NewBotCLIResolver(bots, capabilities, sessions, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_s")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.ResumeSessionID != "conv_42" {
		t.Fatalf("ResumeSessionID = %q, want conv_42", spec.ResumeSessionID)
	}
}

func TestResolveNoStoredSessionLeavesResumeEmpty(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_n", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, &agentSessionRepoStub{}, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_n")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty", spec.ResumeSessionID)
	}
}

type erroringSessionRepoStub struct{}

func (erroringSessionRepoStub) Upsert(context.Context, domain.BotCLISession) error { return nil }
func (erroringSessionRepoStub) Get(context.Context, string, string) (domain.BotCLISession, error) {
	return domain.BotCLISession{}, errors.New("db down")
}

func TestResolveSessionLookupErrorIsNonFatal(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_e", Name: "b", AgentCapabilityID: "cap_codex", AgentMode: "codex-acp"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {ID: "cap_codex", Key: "codex", Command: "codex", SupportedModes: []string{"codex-acp"}, Available: true},
	}}
	r := NewBotCLIResolver(bots, capabilities, erroringSessionRepoStub{}, BotCLIResolverConfig{})
	spec, err := r.Resolve(context.Background(), "bot_e")
	if err != nil {
		t.Fatalf("session lookup error must be non-fatal, got %v", err)
	}
	if spec.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty on lookup error", spec.ResumeSessionID)
	}
}
