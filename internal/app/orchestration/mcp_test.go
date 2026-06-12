package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type fakeRegistry struct{ agents []domain.RegisteredAgent }

func (f fakeRegistry) List(ctx context.Context) ([]domain.RegisteredAgent, error) {
	return f.agents, nil
}
func (f fakeRegistry) GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error) {
	for _, a := range f.agents {
		if a.Name == name {
			return a, nil
		}
	}
	return domain.RegisteredAgent{}, domain.ErrNotFound
}

func newTestService() (*MCPService, *fakeExecutor) {
	exec := &fakeExecutor{resp: agentResponse("sub-answer")}
	reg := fakeRegistry{agents: []domain.RegisteredAgent{{
		Name: "researcher", Description: "web", Kind: domain.RegisteredAgentKindLocal,
		BotID: "bot_sub", Health: domain.RegisteredAgentHealthy,
	}}}
	runner := NewRunner(NewLocalRunner(fakeResolver{}, exec), nil)
	svc := NewMCPService(reg, NewTaskStore(), runner)
	return svc, exec
}

func TestMCPListAgents(t *testing.T) {
	svc, _ := newTestService()
	out, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Agents) != 1 || out.Agents[0].Name != "researcher" {
		t.Fatalf("unexpected agents: %+v", out.Agents)
	}
}

func TestMCPDispatchThenGetTask(t *testing.T) {
	svc, _ := newTestService()
	disp, err := svc.Dispatch(context.Background(), DispatchInput{AgentName: "researcher", Prompt: "find X"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if disp.TaskID == "" {
		t.Fatal("expected task id")
	}

	// Poll until terminal (the runner runs in a goroutine).
	deadline := time.Now().Add(2 * time.Second)
	var got GetTaskOutput
	for time.Now().Before(deadline) {
		got, err = svc.GetTask(context.Background(), GetTaskInput{TaskID: disp.TaskID})
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if got.State == string(TaskStateCompleted) || got.State == string(TaskStateFailed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.State != string(TaskStateCompleted) || got.Result != "sub-answer" {
		t.Fatalf("unexpected final task: %+v", got)
	}
}

func TestMCPDispatchUnknownAgent(t *testing.T) {
	svc, _ := newTestService()
	if _, err := svc.Dispatch(context.Background(), DispatchInput{AgentName: "ghost", Prompt: "x"}); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}
