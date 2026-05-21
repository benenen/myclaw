package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

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
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{Timeout: 45 * time.Second})

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
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{
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
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{
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
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{
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
	resolver := NewBotCLIResolver(newBotRepoStub(domain.Bot{ID: "bot_1"}), &agentCapabilityRepoStub{}, BotCLIResolverConfig{})

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
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

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
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if !errors.Is(err, ErrBotCLIUnsupportedMode) {
		t.Fatalf("expected ErrBotCLIUnsupportedMode, got %v", err)
	}
}

func TestBotCLIResolverResolveReturnsCapabilityLookupError(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_codex", AgentMode: "codex-exec"})
	capabilities := &agentCapabilityRepoStub{getByIDErr: errors.New("lookup failed")}
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if err == nil || err.Error() != "lookup failed" {
		t.Fatalf("expected lookup failed, got %v", err)
	}
}
