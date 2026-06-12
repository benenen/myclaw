package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	"github.com/benenen/myclaw/internal/domain"
)

// RegisterHandler upserts a remote sub-agent from its self-registration call.
func RegisterHandler(reg Upserter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.RegisterAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Endpoint) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "name and endpoint are required")
			return
		}
		now := time.Now().UTC()
		stored, err := reg.Upsert(r.Context(), domain.RegisteredAgent{
			ID:            domain.NewPrefixedID("ra"),
			Name:          req.Name,
			Description:   req.Description,
			Kind:          domain.RegisteredAgentKindRemote,
			Endpoint:      req.Endpoint,
			AuthToken:     req.AuthToken,
			Health:        domain.RegisteredAgentHealthy,
			LastHeartbeat: &now,
		})
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		httpapi.WriteOKFromRequest(w, r, dto.RegisterAgentResponse{AgentID: stored.ID, Name: stored.Name})
	}
}

// HeartbeatHandler refreshes a remote agent's health/last-heartbeat.
func HeartbeatHandler(reg interface {
	Upserter
	GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error)
}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "name is required")
			return
		}
		existing, err := reg.GetByName(r.Context(), req.Name)
		if err != nil {
			httpapi.WriteError(w, r, "NOT_FOUND", "agent not registered")
			return
		}
		now := time.Now().UTC()
		existing.Health = domain.RegisteredAgentHealthy
		existing.LastHeartbeat = &now
		if _, err := reg.Upsert(r.Context(), existing); err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		httpapi.WriteOKFromRequest(w, r, map[string]any{"ok": true})
	}
}
