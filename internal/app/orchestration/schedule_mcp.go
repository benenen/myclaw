package orchestration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

// minScheduleInterval keeps agents from hammering their own session (and the
// IM channel) with sub-second tick loops.
const minScheduleInterval = time.Second

// TaskScheduler is the scheduling side of the agent session manager.
// Satisfied by *agent.Manager. Schedule attaches to the bot's existing session,
// so it takes no spec and never blocks on the turn lock.
type TaskScheduler interface {
	Schedule(botID string, task agent.ScheduledTask) (string, error)
	CancelTask(botID, taskID string) bool
	Tasks(botID string) []agent.ScheduledTask
}

// SetScheduler wires the scheduled-task tools. Until it is called the
// schedule_task/cancel_scheduled_task/list_scheduled_tasks tools return a
// configuration error.
func (s *MCPService) SetScheduler(scheduler TaskScheduler) {
	s.scheduler = scheduler
}

type botIDKey struct{}

// WithBotID marks the context with the identity of the bot making the tool
// call. The HTTP layer derives it from the per-bot MCP URL (?bot_id=...).
func WithBotID(ctx context.Context, botID string) context.Context {
	return context.WithValue(ctx, botIDKey{}, botID)
}

// BotIDFromContext returns the calling bot's id, or "" when unknown.
func BotIDFromContext(ctx context.Context) string {
	botID, _ := ctx.Value(botIDKey{}).(string)
	return botID
}

// botIDMiddleware lifts ?bot_id=... into the request context so tool handlers
// can identify the calling bot.
func botIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if botID := r.URL.Query().Get("bot_id"); botID != "" {
			r = r.WithContext(WithBotID(r.Context(), botID))
		}
		next.ServeHTTP(w, r)
	})
}

// ---- tool I/O types ----

type ScheduleTaskInput struct {
	Interval string `json:"interval" jsonschema:"how often to run the prompt, as a Go duration such as 30s, 1m, 2h; minimum 1s"`
	Prompt   string `json:"prompt" jsonschema:"the self-contained prompt to run on every tick; each result is pushed to the bot's chat"`
}

type ScheduleTaskOutput struct {
	TaskID string `json:"task_id" jsonschema:"cancel with cancel_scheduled_task"`
}

type CancelScheduledTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"a task id returned by schedule_task or list_scheduled_tasks"`
}

type CancelScheduledTaskOutput struct {
	Canceled bool `json:"canceled"`
}

type ScheduledTaskInfo struct {
	TaskID   string `json:"task_id"`
	Interval string `json:"interval"`
	Prompt   string `json:"prompt"`
}

type ListScheduledTasksOutput struct {
	Tasks []ScheduledTaskInfo `json:"tasks" jsonschema:"the bot's active scheduled tasks"`
}

// ---- tool implementations ----

func (s *MCPService) schedulerFor(ctx context.Context) (TaskScheduler, string, error) {
	if s.scheduler == nil {
		return nil, "", fmt.Errorf("scheduled tasks are not configured on this server")
	}
	botID := BotIDFromContext(ctx)
	if botID == "" {
		return nil, "", fmt.Errorf("bot identity missing: the MCP URL must include bot_id")
	}
	return s.scheduler, botID, nil
}

// ScheduleTask registers a periodic prompt on the calling bot's own session.
func (s *MCPService) ScheduleTask(ctx context.Context, in ScheduleTaskInput) (ScheduleTaskOutput, error) {
	scheduler, botID, err := s.schedulerFor(ctx)
	if err != nil {
		return ScheduleTaskOutput{}, err
	}
	interval, err := time.ParseDuration(strings.TrimSpace(in.Interval))
	if err != nil {
		return ScheduleTaskOutput{}, fmt.Errorf("invalid interval %q: use a Go duration such as 30s or 1m", in.Interval)
	}
	if interval < minScheduleInterval {
		return ScheduleTaskOutput{}, fmt.Errorf("interval %s is below the minimum %s", interval, minScheduleInterval)
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return ScheduleTaskOutput{}, fmt.Errorf("prompt is required")
	}
	taskID, err := scheduler.Schedule(botID, agent.ScheduledTask{Interval: interval, Prompt: in.Prompt})
	if err != nil {
		return ScheduleTaskOutput{}, err
	}
	return ScheduleTaskOutput{TaskID: taskID}, nil
}

// CancelScheduledTask cancels one of the calling bot's scheduled tasks.
func (s *MCPService) CancelScheduledTask(ctx context.Context, in CancelScheduledTaskInput) (CancelScheduledTaskOutput, error) {
	scheduler, botID, err := s.schedulerFor(ctx)
	if err != nil {
		return CancelScheduledTaskOutput{}, err
	}
	return CancelScheduledTaskOutput{Canceled: scheduler.CancelTask(botID, in.TaskID)}, nil
}

// ListScheduledTasks lists the calling bot's active scheduled tasks.
func (s *MCPService) ListScheduledTasks(ctx context.Context) (ListScheduledTasksOutput, error) {
	scheduler, botID, err := s.schedulerFor(ctx)
	if err != nil {
		return ListScheduledTasksOutput{}, err
	}
	tasks := scheduler.Tasks(botID)
	out := ListScheduledTasksOutput{Tasks: make([]ScheduledTaskInfo, 0, len(tasks))}
	for _, task := range tasks {
		out.Tasks = append(out.Tasks, ScheduledTaskInfo{
			TaskID:   task.ID,
			Interval: task.Interval.String(),
			Prompt:   task.Prompt,
		})
	}
	return out, nil
}
