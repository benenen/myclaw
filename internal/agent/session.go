package agent

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"
)

type Session struct {
	mu      sync.Mutex
	state   SessionState
	runtime SessionRuntime
	spec    Spec

	// Scheduler state uses its own mutex: mu is held for the whole duration of
	// a turn, so guarding these with mu would block registration during runs.
	schedMu     sync.Mutex
	sink        PushSink
	tasks       map[string]*taskTimer
	taskSeq     int
	schedClosed bool
}

// taskTimer is one scheduled task's stop handle.
type taskTimer struct {
	task    ScheduledTask
	stopCh  chan struct{}
	stopped sync.Once
}

func (t *taskTimer) stop() {
	t.stopped.Do(func() { close(t.stopCh) })
}

func NewSession(ctx context.Context, driver Driver, spec Spec) (*Session, error) {
	clonedSpec := cloneSpec(spec)
	runtime, err := driver.Init(ctx, clonedSpec)
	if err != nil {
		return nil, err
	}
	return &Session{
		state:   SessionStateReady,
		runtime: runtime,
		spec:    clonedSpec,
	}, nil
}

func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) Send(ctx context.Context, req Request) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runLocked(ctx, req)
}

// runLocked runs one turn; the caller must hold s.mu.
func (s *Session) runLocked(ctx context.Context, req Request) (Response, error) {
	s.state = SessionStateBusy
	resp, err := s.runtime.Run(ctx, req)
	if err != nil {
		s.state = SessionStateBroken
		return resp, err
	}

	s.state = SessionStateReady
	return resp, nil
}

// SetPushSink registers the receiver for scheduled-task responses. A nil sink
// drops future pushes.
func (s *Session) SetPushSink(sink PushSink) {
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	s.sink = sink
}

func (s *Session) pushSink() PushSink {
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	return s.sink
}

// Schedule registers a periodic task on this session and starts its timer.
// Tasks live as long as the session: Close or a broken session stops them.
func (s *Session) Schedule(task ScheduledTask) (string, error) {
	if task.Interval <= 0 {
		return "", fmt.Errorf("scheduled task interval must be positive")
	}
	if strings.TrimSpace(task.Prompt) == "" {
		return "", fmt.Errorf("scheduled task prompt is required")
	}

	s.schedMu.Lock()
	if s.schedClosed {
		s.schedMu.Unlock()
		return "", fmt.Errorf("session is closed")
	}
	if task.ID == "" {
		s.taskSeq++
		task.ID = fmt.Sprintf("task-%d", s.taskSeq)
	}
	if _, exists := s.tasks[task.ID]; exists {
		s.schedMu.Unlock()
		return "", fmt.Errorf("scheduled task already exists: %s", task.ID)
	}
	if s.tasks == nil {
		s.tasks = make(map[string]*taskTimer)
	}
	timer := &taskTimer{task: task, stopCh: make(chan struct{})}
	s.tasks[task.ID] = timer
	s.schedMu.Unlock()

	go s.runTask(timer)
	return task.ID, nil
}

// CancelTask stops and removes one task. It returns false for unknown ids.
func (s *Session) CancelTask(taskID string) bool {
	s.schedMu.Lock()
	timer, ok := s.tasks[taskID]
	if ok {
		delete(s.tasks, taskID)
	}
	s.schedMu.Unlock()
	if !ok {
		return false
	}
	timer.stop()
	return true
}

// Tasks returns a snapshot of the active scheduled tasks.
func (s *Session) Tasks() []ScheduledTask {
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	out := make([]ScheduledTask, 0, len(s.tasks))
	for _, timer := range s.tasks {
		out = append(out, timer.task)
	}
	return out
}

// stopAllTasks stops every timer; markClosed additionally rejects future
// Schedule calls (used by Close).
func (s *Session) stopAllTasks(markClosed bool) {
	s.schedMu.Lock()
	if markClosed {
		s.schedClosed = true
	}
	timers := make([]*taskTimer, 0, len(s.tasks))
	for id, timer := range s.tasks {
		timers = append(timers, timer)
		delete(s.tasks, id)
	}
	s.schedMu.Unlock()
	for _, timer := range timers {
		timer.stop()
	}
}

// runTask drives one task's ticker until the task is cancelled, the session
// closes, or the session breaks.
func (s *Session) runTask(t *taskTimer) {
	ticker := time.NewTicker(t.task.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			if !s.runTick(t.task) {
				s.CancelTask(t.task.ID)
				return
			}
		}
	}
}

// runTick runs one synthesized turn for the task and pushes its response.
// A busy session skips the tick (TryLock — never queue behind an in-flight
// turn; State() would block on mu for the whole turn). It reports false when
// the session is broken or closed and the task must stop.
func (s *Session) runTick(task ScheduledTask) bool {
	if !s.mu.TryLock() {
		return true // a turn is in flight: skip this tick
	}
	if s.runtime == nil || s.state == SessionStateBroken || s.state == SessionStateStopped {
		s.mu.Unlock()
		return false
	}

	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	if s.spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.spec.Timeout)
	}
	resp, err := s.runLocked(ctx, Request{BotID: s.spec.BotID, Prompt: task.Prompt})
	cancel()
	s.mu.Unlock()
	if err != nil {
		slog.Error("scheduled task tick failed", "bot_id", s.spec.BotID, "task_id", task.ID, "error", err)
		return false
	}

	if sink := s.pushSink(); sink != nil {
		sink(PushResponse{Response: resp, TaskID: task.ID})
	}
	return true
}

func (s *Session) Matches(spec Spec) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return reflect.DeepEqual(s.spec, spec)
}

func (s *Session) Close() error {
	// Stop scheduled tasks first so no new tick can start once teardown of the
	// runtime begins; Close intent also blocks future Schedule calls.
	s.stopAllTasks(true)

	s.mu.Lock()
	runtime := s.runtime
	if runtime == nil {
		s.state = SessionStateStopped
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if err := runtime.Close(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil {
		s.runtime = nil
		s.state = SessionStateStopped
	}
	return nil
}

func cloneSpec(spec Spec) Spec {
	cloned := spec
	if spec.Args != nil {
		cloned.Args = append([]string(nil), spec.Args...)
	}
	if spec.Env != nil {
		cloned.Env = make(map[string]string, len(spec.Env))
		for k, v := range spec.Env {
			cloned.Env[k] = v
		}
	}
	return cloned
}
