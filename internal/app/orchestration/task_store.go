package orchestration

import (
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/domain"
)

type TaskState string

const (
	TaskStateSubmitted TaskState = "submitted"
	TaskStateWorking   TaskState = "working"
	TaskStateCompleted TaskState = "completed"
	TaskStateFailed    TaskState = "failed"
	TaskStateCanceled  TaskState = "canceled"
)

func (s TaskState) terminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed || s == TaskStateCanceled
}

type Task struct {
	ID        string
	AgentName string
	Prompt    string
	State     TaskState
	Result    string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TaskStore struct {
	mu    sync.Mutex
	tasks map[string]*Task
	now   func() time.Time
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks: make(map[string]*Task),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *TaskStore) Create(agentName, prompt string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	t := &Task{
		ID:        domain.NewPrefixedID("task"),
		AgentName: agentName,
		Prompt:    prompt,
		State:     TaskStateSubmitted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.tasks[t.ID] = t
	return *t
}

func (s *TaskStore) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

func (s *TaskStore) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	return out
}

func (s *TaskStore) SetWorking(id string) { s.transition(id, TaskStateWorking, "", "") }

func (s *TaskStore) Complete(id, result string) { s.transition(id, TaskStateCompleted, result, "") }

func (s *TaskStore) Fail(id, errMsg string) { s.transition(id, TaskStateFailed, "", errMsg) }

func (s *TaskStore) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.State.terminal() {
		return false
	}
	t.State = TaskStateCanceled
	t.UpdatedAt = s.now()
	return true
}

func (s *TaskStore) transition(id string, state TaskState, result, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.State.terminal() {
		return
	}
	t.State = state
	if result != "" {
		t.Result = result
	}
	if errMsg != "" {
		t.Error = errMsg
	}
	t.UpdatedAt = s.now()
}
