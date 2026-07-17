package orchestration

import (
	"context"
	"fmt"
	"net/http"

	"github.com/benenen/myclaw/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registry is the read side of the agent registry the MCP tools need.
type Registry interface {
	List(ctx context.Context) ([]domain.RegisteredAgent, error)
	GetByName(ctx context.Context, name string) (domain.RegisteredAgent, error)
}

type MCPService struct {
	registry Registry
	tasks    *TaskStore
	runner   *Runner
	// Scheduled-task tools; nil until SetScheduler wires them.
	scheduler TaskScheduler
}

func NewMCPService(registry Registry, tasks *TaskStore, runner *Runner) *MCPService {
	return &MCPService{registry: registry, tasks: tasks, runner: runner}
}

// ---- tool I/O types (schemas are generated from these by the SDK) ----

type AgentInfo struct {
	Name        string `json:"name" jsonschema:"the dispatch name of the sub-agent"`
	Description string `json:"description" jsonschema:"what the sub-agent is good at"`
	Kind        string `json:"kind" jsonschema:"local or remote"`
	Health      string `json:"health" jsonschema:"healthy or unhealthy"`
}

type ListAgentsOutput struct {
	Agents []AgentInfo `json:"agents" jsonschema:"the available sub-agents"`
}

type DispatchInput struct {
	AgentName string `json:"agent_name" jsonschema:"name of the sub-agent to run (from list_agents)"`
	Prompt    string `json:"prompt" jsonschema:"the self-contained subtask instructions"`
}

type DispatchOutput struct {
	TaskID string `json:"task_id" jsonschema:"poll this id with get_task"`
}

type GetTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"a task id returned by dispatch"`
}

type GetTaskOutput struct {
	TaskID    string `json:"task_id"`
	AgentName string `json:"agent_name"`
	State     string `json:"state" jsonschema:"submitted, working, completed, failed, or canceled"`
	Result    string `json:"result" jsonschema:"final output when completed"`
	Error     string `json:"error" jsonschema:"error message when failed"`
}

type CancelInput struct {
	TaskID string `json:"task_id"`
}

type CancelOutput struct {
	Canceled bool `json:"canceled"`
}

// ---- tool implementations (testable without a transport) ----

func (s *MCPService) ListAgents(ctx context.Context) (ListAgentsOutput, error) {
	agents, err := s.registry.List(ctx)
	if err != nil {
		return ListAgentsOutput{}, err
	}
	out := ListAgentsOutput{Agents: make([]AgentInfo, 0, len(agents))}
	for _, a := range agents {
		if a.Health == domain.RegisteredAgentUnhealthy {
			continue
		}
		out.Agents = append(out.Agents, AgentInfo{
			Name: a.Name, Description: a.Description, Kind: a.Kind, Health: a.Health,
		})
	}
	return out, nil
}

func (s *MCPService) Dispatch(ctx context.Context, in DispatchInput) (DispatchOutput, error) {
	ra, err := s.registry.GetByName(ctx, in.AgentName)
	if err != nil {
		return DispatchOutput{}, fmt.Errorf("unknown agent %q: %w", in.AgentName, err)
	}
	task := s.tasks.Create(ra.Name, in.Prompt)
	go func() {
		s.tasks.SetWorking(task.ID)
		// Detached from the inbound request context so it outlives the tool call.
		result, runErr := s.runner.Run(context.Background(), ra, in.Prompt)
		if runErr != nil {
			s.tasks.Fail(task.ID, runErr.Error())
			return
		}
		s.tasks.Complete(task.ID, result)
	}()
	return DispatchOutput{TaskID: task.ID}, nil
}

func (s *MCPService) GetTask(ctx context.Context, in GetTaskInput) (GetTaskOutput, error) {
	t, ok := s.tasks.Get(in.TaskID)
	if !ok {
		return GetTaskOutput{}, fmt.Errorf("unknown task %q", in.TaskID)
	}
	return GetTaskOutput{
		TaskID: t.ID, AgentName: t.AgentName, State: string(t.State),
		Result: t.Result, Error: t.Error,
	}, nil
}

func (s *MCPService) CancelTask(ctx context.Context, in CancelInput) (CancelOutput, error) {
	return CancelOutput{Canceled: s.tasks.Cancel(in.TaskID)}, nil
}

// NewMCPHandler builds the MCP server, registers the four tools, and returns an
// http.Handler to mount (e.g. at /mcp).
func NewMCPHandler(svc *MCPService) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "myclaw", Version: "1.0.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_agents",
		Description: "List the sub-agents you can dispatch subtasks to.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListAgentsOutput, error) {
		out, err := svc.ListAgents(ctx)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "dispatch",
		Description: "Dispatch a self-contained subtask to a sub-agent. Returns a task_id immediately; poll it with get_task.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DispatchInput) (*mcp.CallToolResult, DispatchOutput, error) {
		out, err := svc.Dispatch(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_task",
		Description: "Get the current state and result of a dispatched task.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetTaskInput) (*mcp.CallToolResult, GetTaskOutput, error) {
		out, err := svc.GetTask(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cancel",
		Description: "Cancel a non-terminal task.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CancelInput) (*mcp.CallToolResult, CancelOutput, error) {
		out, err := svc.CancelTask(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "schedule_task",
		Description: "Schedule a recurring prompt on your own session (e.g. report CPU usage every minute). Each tick's result is pushed to your chat. Returns a task_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ScheduleTaskInput) (*mcp.CallToolResult, ScheduleTaskOutput, error) {
		out, err := svc.ScheduleTask(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cancel_scheduled_task",
		Description: "Cancel one of your scheduled recurring tasks by task_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CancelScheduledTaskInput) (*mcp.CallToolResult, CancelScheduledTaskOutput, error) {
		out, err := svc.CancelScheduledTask(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_scheduled_tasks",
		Description: "List your active scheduled recurring tasks.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListScheduledTasksOutput, error) {
		out, err := svc.ListScheduledTasks(ctx)
		return nil, out, err
	})

	return botIDMiddleware(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: false}))
}
