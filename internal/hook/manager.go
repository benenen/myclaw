package hook

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

// botRepository is the subset of BotRepository needed by the Manager.
type botRepository interface {
	GetByName(ctx context.Context, name string) (domain.Bot, error)
}

// specResolver resolves a bot ID to an agent Spec.
type specResolver interface {
	Resolve(ctx context.Context, botID string) (agent.Spec, error)
}

// executor sends prompts to agents.
type executor interface {
	Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error)
}

// Manager routes incoming webhook requests to the appropriate Hook
// implementation, looks up the corresponding Bot, and delegates
// processing to the agent system.
type Manager struct {
	hooks    map[string]Hook
	botRepo  botRepository
	resolver specResolver
	executor executor
}

// NewManager creates a new hook Manager.
func NewManager(botRepo botRepository, resolver specResolver, executor executor) *Manager {
	return &Manager{
		hooks:    make(map[string]Hook),
		botRepo:  botRepo,
		resolver: resolver,
		executor: executor,
	}
}

// RegisterHook registers a platform-specific hook handler.
func (m *Manager) RegisterHook(hook Hook) {
	m.hooks[hook.ID()] = hook
}

// HandleHook processes an incoming webhook request for the given platform ID.
// It looks up the registered Hook, validates the request, finds the
// corresponding Bot, sends the prompt to the agent, and returns the result.
// If no specific Hook is registered, it falls back to passthrough mode:
// reading the raw request body and sending it as the prompt to the Bot
// whose name matches the platform ID.
func (m *Manager) HandleHook(w stdhttp.ResponseWriter, r *stdhttp.Request, platformID string) {
	hook, ok := m.hooks[platformID]
	if ok {
		m.handleWithHook(w, r, platformID, hook)
		return
	}
	m.handlePassthrough(w, r, platformID)
}

func (m *Manager) handleWithHook(w stdhttp.ResponseWriter, r *stdhttp.Request, platformID string, h Hook) {
	prompt, err := h.Handle(r.Context(), r)
	if err != nil {
		httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
		return
	}

	bot, err := m.botRepo.GetByName(r.Context(), platformID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			httpapi.WriteError(w, r, "NOT_FOUND", "no bot configured for platform: "+platformID)
			return
		}
		httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
		return
	}

	m.sendToAgent(w, r, bot, prompt)
}

func (m *Manager) handlePassthrough(w stdhttp.ResponseWriter, r *stdhttp.Request, platformID string) {
	bot, err := m.botRepo.GetByName(r.Context(), platformID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			httpapi.WriteError(w, r, "NOT_FOUND", "no hook or bot configured for platform: "+platformID)
			return
		}
		httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpapi.WriteError(w, r, "INVALID_ARGUMENT", "failed to read request body")
		return
	}

	m.sendToAgent(w, r, bot, string(body))
}

func (m *Manager) sendToAgent(w stdhttp.ResponseWriter, r *stdhttp.Request, bot domain.Bot, prompt string) {
	spec, err := m.resolver.Resolve(r.Context(), bot.ID)
	if err != nil {
		httpapi.WriteError(w, r, "INTERNAL_ERROR", "failed to resolve agent spec: "+err.Error())
		return
	}

	resp, err := m.executor.Send(r.Context(), bot.ID, spec, agent.Request{Prompt: prompt})
	if err != nil {
		httpapi.WriteError(w, r, "INTERNAL_ERROR", "agent execution failed: "+err.Error())
		return
	}

	httpapi.WriteOKFromRequest(w, r, map[string]any{"text": resp.Text})
}
