package orchestration

import (
	"context"
	"fmt"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

// SpecResolver resolves a sub-agent Bot into an agent.Spec.
// Satisfied by *bot.BotCLIResolver.
type SpecResolver interface {
	Resolve(ctx context.Context, botID string) (agent.Spec, error)
}

// Executor runs one turn against a bot session. Satisfied by *agent.Manager.
type Executor interface {
	Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error)
}

// RemoteRunner is the A2A HTTP path; supplied in M6. nil until then.
type RemoteRunner interface {
	Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error)
}

type LocalRunner struct {
	resolver SpecResolver
	executor Executor
}

func NewLocalRunner(resolver SpecResolver, executor Executor) *LocalRunner {
	return &LocalRunner{resolver: resolver, executor: executor}
}

func (l *LocalRunner) Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error) {
	spec, err := l.resolver.Resolve(ctx, a.BotID)
	if err != nil {
		return "", fmt.Errorf("resolve sub-agent %s: %w", a.BotID, err)
	}
	resp, err := l.executor.Send(ctx, a.BotID, spec, agent.Request{
		BotID:     a.BotID,
		MessageID: domain.NewPrefixedID("subtask"),
		Prompt:    prompt,
	})
	if err != nil {
		return resp.Text, err
	}
	return resp.Text, nil
}

// Runner dispatches to the correct backend based on RegisteredAgent.Kind.
type Runner struct {
	local  *LocalRunner
	remote RemoteRunner
}

func NewRunner(local *LocalRunner, remote RemoteRunner) *Runner {
	return &Runner{local: local, remote: remote}
}

func (r *Runner) Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error) {
	switch a.Kind {
	case domain.RegisteredAgentKindLocal:
		if r.local == nil {
			return "", fmt.Errorf("local runner not configured")
		}
		return r.local.Run(ctx, a, prompt)
	case domain.RegisteredAgentKindRemote:
		if r.remote == nil {
			return "", fmt.Errorf("remote runner not configured")
		}
		return r.remote.Run(ctx, a, prompt)
	default:
		return "", fmt.Errorf("unknown agent kind %q", a.Kind)
	}
}
