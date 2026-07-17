package orchestration

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeScheduler struct {
	mu        sync.Mutex
	botIDs    []string
	specs     []agent.Spec
	scheduled []agent.ScheduledTask
	canceled  []string
	cancelOK  bool
	tasks     map[string][]agent.ScheduledTask
}

func (f *fakeScheduler) Schedule(_ context.Context, botID string, spec agent.Spec, task agent.ScheduledTask) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.botIDs = append(f.botIDs, botID)
	f.specs = append(f.specs, spec)
	f.scheduled = append(f.scheduled, task)
	return "task-1", nil
}

func (f *fakeScheduler) CancelTask(botID, taskID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.botIDs = append(f.botIDs, botID)
	f.canceled = append(f.canceled, taskID)
	return f.cancelOK
}

func (f *fakeScheduler) Tasks(botID string) []agent.ScheduledTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.botIDs = append(f.botIDs, botID)
	return f.tasks[botID]
}

func newScheduleTestService(sched *fakeScheduler) *MCPService {
	svc, _ := newTestService()
	svc.SetScheduler(sched, fakeResolver{spec: agent.Spec{Command: "claude"}})
	return svc
}

func TestMCPScheduleTask(t *testing.T) {
	sched := &fakeScheduler{}
	svc := newScheduleTestService(sched)

	ctx := WithBotID(context.Background(), "bot_1")
	out, err := svc.ScheduleTask(ctx, ScheduleTaskInput{Interval: "1m", Prompt: "check cpu"})
	if err != nil {
		t.Fatalf("ScheduleTask() error = %v", err)
	}
	if out.TaskID != "task-1" {
		t.Fatalf("TaskID = %q", out.TaskID)
	}
	if len(sched.scheduled) != 1 {
		t.Fatalf("scheduled = %#v", sched.scheduled)
	}
	if sched.botIDs[0] != "bot_1" {
		t.Fatalf("botID = %q", sched.botIDs[0])
	}
	if sched.scheduled[0].Interval != time.Minute {
		t.Fatalf("interval = %v", sched.scheduled[0].Interval)
	}
	if sched.scheduled[0].Prompt != "check cpu" {
		t.Fatalf("prompt = %q", sched.scheduled[0].Prompt)
	}
	if sched.specs[0].Command != "claude" {
		t.Fatalf("spec.Command = %q (expected resolver spec)", sched.specs[0].Command)
	}
}

func TestMCPScheduleTaskRejectsBadInput(t *testing.T) {
	svc := newScheduleTestService(&fakeScheduler{})
	ctx := WithBotID(context.Background(), "bot_1")

	if _, err := svc.ScheduleTask(context.Background(), ScheduleTaskInput{Interval: "1m", Prompt: "p"}); err == nil {
		t.Fatal("expected error without bot identity")
	}
	if _, err := svc.ScheduleTask(ctx, ScheduleTaskInput{Interval: "soon", Prompt: "p"}); err == nil {
		t.Fatal("expected error for unparsable interval")
	}
	if _, err := svc.ScheduleTask(ctx, ScheduleTaskInput{Interval: "100ms", Prompt: "p"}); err == nil {
		t.Fatal("expected error for interval below minimum")
	}
	if _, err := svc.ScheduleTask(ctx, ScheduleTaskInput{Interval: "1m", Prompt: "  "}); err == nil {
		t.Fatal("expected error for blank prompt")
	}
}

func TestMCPScheduleToolsRequireScheduler(t *testing.T) {
	svc, _ := newTestService() // no SetScheduler
	ctx := WithBotID(context.Background(), "bot_1")
	if _, err := svc.ScheduleTask(ctx, ScheduleTaskInput{Interval: "1m", Prompt: "p"}); err == nil {
		t.Fatal("expected error when scheduler is not configured")
	}
	if _, err := svc.CancelScheduledTask(ctx, CancelScheduledTaskInput{TaskID: "t"}); err == nil {
		t.Fatal("expected error when scheduler is not configured")
	}
	if _, err := svc.ListScheduledTasks(ctx); err == nil {
		t.Fatal("expected error when scheduler is not configured")
	}
}

func TestMCPCancelScheduledTask(t *testing.T) {
	sched := &fakeScheduler{cancelOK: true}
	svc := newScheduleTestService(sched)
	ctx := WithBotID(context.Background(), "bot_1")

	out, err := svc.CancelScheduledTask(ctx, CancelScheduledTaskInput{TaskID: "task-9"})
	if err != nil {
		t.Fatalf("CancelScheduledTask() error = %v", err)
	}
	if !out.Canceled {
		t.Fatal("Canceled = false")
	}
	if sched.botIDs[0] != "bot_1" || sched.canceled[0] != "task-9" {
		t.Fatalf("cancel call = %v %v", sched.botIDs, sched.canceled)
	}

	sched.cancelOK = false
	out, err = svc.CancelScheduledTask(ctx, CancelScheduledTaskInput{TaskID: "task-9"})
	if err != nil {
		t.Fatalf("CancelScheduledTask() error = %v", err)
	}
	if out.Canceled {
		t.Fatal("Canceled = true for unknown task")
	}
}

func TestMCPListScheduledTasks(t *testing.T) {
	sched := &fakeScheduler{tasks: map[string][]agent.ScheduledTask{
		"bot_1": {{ID: "task-1", Interval: time.Minute, Prompt: "check cpu"}},
	}}
	svc := newScheduleTestService(sched)
	ctx := WithBotID(context.Background(), "bot_1")

	out, err := svc.ListScheduledTasks(ctx)
	if err != nil {
		t.Fatalf("ListScheduledTasks() error = %v", err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("Tasks = %#v", out.Tasks)
	}
	got := out.Tasks[0]
	if got.TaskID != "task-1" || got.Interval != "1m0s" || got.Prompt != "check cpu" {
		t.Fatalf("task = %#v", got)
	}
}

// TestMCPHandlerPropagatesBotIDFromQuery proves the full path: the per-bot MCP
// URL carries ?bot_id=..., the handler middleware lifts it into the context,
// and the schedule_task tool sees it.
func TestMCPHandlerPropagatesBotIDFromQuery(t *testing.T) {
	sched := &fakeScheduler{}
	svc := newScheduleTestService(sched)
	srv := httptest.NewServer(NewMCPHandler(svc))
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "?bot_id=bot_42"}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "schedule_task",
		Arguments: map[string]any{"interval": "1m", "prompt": "check cpu"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool() tool error: %#v", res.Content)
	}

	sched.mu.Lock()
	defer sched.mu.Unlock()
	if len(sched.botIDs) != 1 || sched.botIDs[0] != "bot_42" {
		t.Fatalf("scheduler botIDs = %v, want [bot_42]", sched.botIDs)
	}
}

func TestBotIDContextHelpers(t *testing.T) {
	if got := BotIDFromContext(context.Background()); got != "" {
		t.Fatalf("BotIDFromContext(empty) = %q", got)
	}
	ctx := WithBotID(context.Background(), "bot_7")
	if got := BotIDFromContext(ctx); got != "bot_7" {
		t.Fatalf("BotIDFromContext = %q", got)
	}
	if !strings.HasPrefix("bot_7", "bot_") {
		t.Fatal("sanity")
	}
}
