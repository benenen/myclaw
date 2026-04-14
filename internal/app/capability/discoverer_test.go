package capability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type discoveryCapabilityRepoStub struct {
	byID       map[string]domain.AgentCapability
	byKey      map[string]domain.AgentCapability
	list       []domain.AgentCapability
	getByIDErr error
	listErr    error
	upserts    []domain.AgentCapability
}

func (r *discoveryCapabilityRepoStub) Upsert(_ context.Context, capability domain.AgentCapability) (domain.AgentCapability, error) {
	updated := capability
	if updated.ID == "" {
		updated.ID = "cap_" + capability.Key
	}
	if r.byID == nil {
		r.byID = map[string]domain.AgentCapability{}
	}
	if r.byKey == nil {
		r.byKey = map[string]domain.AgentCapability{}
	}
	r.byID[updated.ID] = updated
	r.byKey[updated.Key] = updated
	r.upserts = append(r.upserts, updated)
	found := false
	for idx := range r.list {
		if r.list[idx].Key == updated.Key {
			r.list[idx] = updated
			found = true
			break
		}
	}
	if !found {
		r.list = append(r.list, updated)
	}
	return updated, nil
}

func (r *discoveryCapabilityRepoStub) GetByID(_ context.Context, id string) (domain.AgentCapability, error) {
	if r.getByIDErr != nil {
		return domain.AgentCapability{}, r.getByIDErr
	}
	if capability, ok := r.byID[id]; ok {
		return capability, nil
	}
	return domain.AgentCapability{}, domain.ErrNotFound
}

func (r *discoveryCapabilityRepoStub) GetByKey(_ context.Context, key string) (domain.AgentCapability, error) {
	if capability, ok := r.byKey[key]; ok {
		return capability, nil
	}
	return domain.AgentCapability{}, domain.ErrNotFound
}

func (r *discoveryCapabilityRepoStub) List(context.Context) ([]domain.AgentCapability, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return append([]domain.AgentCapability(nil), r.list...), nil
}

func TestAgentCapabilityDiscovererRefreshesCurrentEnvironment(t *testing.T) {
	repo := &discoveryCapabilityRepoStub{}
	discoverer := NewAgentCapabilityDiscoverer(repo, func(name string) (string, error) {
		switch name {
		case "codex":
			return "/usr/local/bin/codex", nil
		case "claude":
			return "", errors.New("not found")
		default:
			t.Fatalf("unexpected command lookup: %s", name)
			return "", nil
		}
	})

	items, err := discoverer.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(items))
	}

	codex, err := repo.GetByKey(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if codex.Command != "/usr/local/bin/codex" {
		t.Fatalf("unexpected codex command: %q", codex.Command)
	}
	if !codex.Available {
		t.Fatal("expected codex available")
	}
	if codex.DetectionSource != "path_scan" {
		t.Fatalf("unexpected detection source: %q", codex.DetectionSource)
	}
	if codex.LastDetectedAt == nil || codex.LastDetectedAt.IsZero() {
		t.Fatal("expected codex last_detected_at")
	}

	claude, err := repo.GetByKey(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if claude.Available {
		t.Fatal("expected claude unavailable")
	}
	if claude.Command != "claude" {
		t.Fatalf("unexpected claude command: %q", claude.Command)
	}
	if claude.LastDetectedAt != nil {
		t.Fatalf("expected nil last_detected_at, got %v", claude.LastDetectedAt)
	}
}

func TestAgentCapabilityDiscovererPreservesLastDetectedAtWhenCommandMissing(t *testing.T) {
	lastDetectedAt := time.Now().UTC().Add(-time.Hour)
	repo := &discoveryCapabilityRepoStub{
		byKey: map[string]domain.AgentCapability{
			"codex": {
				ID:             "cap_codex",
				Key:            "codex",
				Label:          "Codex CLI",
				Command:        "/old/path/codex",
				SupportedModes: []string{"codex-exec", "session"},
				Available:      true,
				LastDetectedAt: &lastDetectedAt,
			},
		},
		list: []domain.AgentCapability{{
			ID:             "cap_codex",
			Key:            "codex",
			Label:          "Codex CLI",
			Command:        "/old/path/codex",
			SupportedModes: []string{"codex-exec", "session"},
			Available:      true,
			LastDetectedAt: &lastDetectedAt,
		}},
	}
	discoverer := NewAgentCapabilityDiscoverer(repo, func(string) (string, error) {
		return "", errors.New("not found")
	})

	if _, err := discoverer.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	codex, err := repo.GetByKey(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if codex.Available {
		t.Fatal("expected codex unavailable")
	}
	if codex.Command != "codex" {
		t.Fatalf("unexpected codex command fallback: %q", codex.Command)
	}
	if codex.LastDetectedAt == nil || !codex.LastDetectedAt.Equal(lastDetectedAt) {
		t.Fatalf("unexpected last_detected_at: %v", codex.LastDetectedAt)
	}
}
