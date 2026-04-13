package bot

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

func TestBotCLIResolverResolveReturnsConfigForConfiguredAvailableCapability(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{
		ID:                "bot_1",
		AgentCapabilityID: "cap_codex",
		AgentMode:         "oneshot",
	})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_codex": {
			ID:             "cap_codex",
			Command:        "/usr/local/bin/codex",
			Args:           []string{"reply", "--plain"},
			SupportedModes: []string{"oneshot", "session"},
			Available:      true,
		},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{Timeout: 45 * time.Second})

	spec, err := resolver.Resolve(context.Background(), "bot_1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Type != "oneshot" {
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

	spec.Args[0] = "mutated"
	if capabilities.byID["cap_codex"].Args[0] != "reply" {
		t.Fatal("expected resolver to copy args")
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
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_claude", AgentMode: "oneshot"})
	capabilities := &agentCapabilityRepoStub{byID: map[string]domain.AgentCapability{
		"cap_claude": {ID: "cap_claude", Available: false, SupportedModes: []string{"oneshot"}},
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
		"cap_claude": {ID: "cap_claude", Available: true, SupportedModes: []string{"oneshot"}},
	}}
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if !errors.Is(err, ErrBotCLIUnsupportedMode) {
		t.Fatalf("expected ErrBotCLIUnsupportedMode, got %v", err)
	}
}

func TestBotCLIResolverResolveReturnsCapabilityLookupError(t *testing.T) {
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", AgentCapabilityID: "cap_codex", AgentMode: "oneshot"})
	capabilities := &agentCapabilityRepoStub{getByIDErr: errors.New("lookup failed")}
	resolver := NewBotCLIResolver(bots, capabilities, BotCLIResolverConfig{})

	_, err := resolver.Resolve(context.Background(), "bot_1")
	if err == nil || err.Error() != "lookup failed" {
		t.Fatalf("expected lookup failed, got %v", err)
	}
}
