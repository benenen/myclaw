package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/domain"
)

type fakeResolver struct{ spec agent.Spec }

func (f fakeResolver) Resolve(ctx context.Context, botID string) (agent.Spec, error) {
	s := f.spec
	s.BotID = botID
	return s, nil
}

type fakeExecutor struct {
	gotBotID  string
	gotPrompt string
	resp      agent.Response
	err       error
}

func (f *fakeExecutor) Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error) {
	f.gotBotID = botID
	f.gotPrompt = req.Prompt
	return f.resp, f.err
}

func TestLocalRunnerSendsToBot(t *testing.T) {
	exec := &fakeExecutor{resp: agent.Response{Text: "done"}}
	r := NewRunner(NewLocalRunner(fakeResolver{}, exec), nil)

	out, err := r.Run(context.Background(), domain.RegisteredAgent{
		Kind:  domain.RegisteredAgentKindLocal,
		BotID: "bot_sub_1",
	}, "do the thing")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected output %q", out)
	}
	if exec.gotBotID != "bot_sub_1" || exec.gotPrompt != "do the thing" {
		t.Fatalf("executor got bot=%q prompt=%q", exec.gotBotID, exec.gotPrompt)
	}
}

func TestRunnerRejectsUnknownKind(t *testing.T) {
	r := NewRunner(nil, nil)
	if _, err := r.Run(context.Background(), domain.RegisteredAgent{Kind: "weird"}, "x"); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestLocalRunnerPropagatesError(t *testing.T) {
	exec := &fakeExecutor{err: errors.New("boom")}
	r := NewRunner(NewLocalRunner(fakeResolver{}, exec), nil)
	if _, err := r.Run(context.Background(), domain.RegisteredAgent{Kind: domain.RegisteredAgentKindLocal, BotID: "b"}, "x"); err == nil {
		t.Fatal("expected error")
	}
}
