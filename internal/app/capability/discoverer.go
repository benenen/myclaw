package capability

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type pathLookupFunc func(name string) (string, error)

type capabilitySeed struct {
	key            string
	label          string
	command        string
	supportedModes []string
}

type AgentCapabilityDiscoverer struct {
	repo     domain.AgentCapabilityRepository
	lookPath pathLookupFunc
}

var capabilitySeeds = []capabilitySeed{
	{key: "codex", label: "Codex CLI", command: "codex", supportedModes: []string{"codex-exec", "codex-pty"}},
	{key: "claude", label: "Claude Code", command: "claude", supportedModes: []string{}},
}

func NewAgentCapabilityDiscoverer(repo domain.AgentCapabilityRepository, lookPath pathLookupFunc) *AgentCapabilityDiscoverer {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return &AgentCapabilityDiscoverer{repo: repo, lookPath: lookPath}
}

func (d *AgentCapabilityDiscoverer) Refresh(ctx context.Context) ([]domain.AgentCapability, error) {
	items := make([]domain.AgentCapability, 0, len(capabilitySeeds))
	for _, seed := range capabilitySeeds {
		existing, err := d.repo.GetByKey(ctx, seed.key)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}

		now := time.Now().UTC()
		capability := domain.AgentCapability{
			ID:              existing.ID,
			Key:             seed.key,
			Label:           seed.label,
			Command:         seed.command,
			SupportedModes:  append([]string(nil), seed.supportedModes...),
			Available:       false,
			DetectionSource: "path_scan",
			LastDetectedAt:  existing.LastDetectedAt,
		}
		if capability.ID == "" {
			capability.ID = domain.NewPrefixedID("cap")
		}

		resolved, lookupErr := d.lookPath(seed.command)
		if lookupErr == nil && resolved != "" {
			capability.Command = resolved
			capability.Available = true
			capability.LastDetectedAt = &now
		}

		stored, err := d.repo.Upsert(ctx, capability)
		if err != nil {
			return nil, err
		}
		items = append(items, stored)
	}
	return items, nil
}
