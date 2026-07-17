package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newScheduleTestSession(t *testing.T, run func(context.Context, Request) (Response, error)) *Session {
	t.Helper()
	session, err := NewSession(context.Background(), initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
		return runtimeStub{run: run}, nil
	}}, Spec{BotID: "bot-1", Command: "codex"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func waitPush(t *testing.T, pushes <-chan PushResponse) PushResponse {
	t.Helper()
	select {
	case pr := <-pushes:
		return pr
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for push")
		return PushResponse{}
	}
}

func assertNoPushWithin(t *testing.T, pushes <-chan PushResponse, d time.Duration) {
	t.Helper()
	select {
	case pr := <-pushes:
		t.Fatalf("unexpected push: %#v", pr)
	case <-time.After(d):
	}
}

func TestSessionScheduleRunsTaskPeriodicallyAndPushesResponses(t *testing.T) {
	var gotPrompt atomic.Value
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		gotPrompt.Store(req.Prompt)
		return Response{Text: "cpu: 12%", RuntimeType: "codex"}, nil
	})

	pushes := make(chan PushResponse, 16)
	session.SetPushSink(func(pr PushResponse) { pushes <- pr })

	taskID, err := session.Schedule(ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "check cpu"})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if taskID == "" {
		t.Fatal("Schedule() returned empty task id")
	}

	first := waitPush(t, pushes)
	second := waitPush(t, pushes)
	for i, pr := range []PushResponse{first, second} {
		if pr.TaskID != taskID {
			t.Fatalf("push %d TaskID = %q, want %q", i, pr.TaskID, taskID)
		}
		if pr.Text != "cpu: 12%" {
			t.Fatalf("push %d Text = %q", i, pr.Text)
		}
	}
	if got, _ := gotPrompt.Load().(string); got != "check cpu" {
		t.Fatalf("runtime prompt = %q", got)
	}
	if got := session.State(); got != SessionStateReady {
		t.Fatalf("State() between ticks = %q", got)
	}
}

func TestSessionScheduleValidatesTask(t *testing.T) {
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		return Response{Text: req.Prompt}, nil
	})

	if _, err := session.Schedule(ScheduledTask{Interval: 0, Prompt: "p"}); err == nil {
		t.Fatal("expected error for zero interval")
	}
	if _, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "  "}); err == nil {
		t.Fatal("expected error for blank prompt")
	}

	firstID, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "a"})
	if err != nil {
		t.Fatalf("first Schedule() error = %v", err)
	}
	secondID, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "b"})
	if err != nil {
		t.Fatalf("second Schedule() error = %v", err)
	}
	if firstID == secondID {
		t.Fatalf("generated ids collide: %q", firstID)
	}

	if _, err := session.Schedule(ScheduledTask{ID: firstID, Interval: time.Minute, Prompt: "c"}); err == nil {
		t.Fatal("expected error for duplicate task id")
	}
}

func TestSessionTasksListsActiveTasks(t *testing.T) {
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		return Response{Text: req.Prompt}, nil
	})

	aID, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "a"})
	if err != nil {
		t.Fatalf("Schedule(a) error = %v", err)
	}
	bID, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "b"})
	if err != nil {
		t.Fatalf("Schedule(b) error = %v", err)
	}

	tasks := session.Tasks()
	if len(tasks) != 2 {
		t.Fatalf("Tasks() len = %d", len(tasks))
	}
	seen := map[string]string{}
	for _, task := range tasks {
		seen[task.ID] = task.Prompt
	}
	if seen[aID] != "a" || seen[bID] != "b" {
		t.Fatalf("Tasks() = %#v", tasks)
	}

	if !session.CancelTask(aID) {
		t.Fatal("CancelTask(aID) = false")
	}
	tasks = session.Tasks()
	if len(tasks) != 1 || tasks[0].ID != bID {
		t.Fatalf("Tasks() after cancel = %#v", tasks)
	}
}

func TestSessionCancelTaskStopsPushes(t *testing.T) {
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		return Response{Text: req.Prompt}, nil
	})
	pushes := make(chan PushResponse, 64)
	session.SetPushSink(func(pr PushResponse) { pushes <- pr })

	taskID, err := session.Schedule(ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tick"})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	waitPush(t, pushes)

	if !session.CancelTask(taskID) {
		t.Fatal("CancelTask() = false")
	}
	if session.CancelTask(taskID) {
		t.Fatal("second CancelTask() = true")
	}

	// Drain any tick that was already in flight when the task was cancelled,
	// then require silence.
	time.Sleep(50 * time.Millisecond)
	for {
		select {
		case <-pushes:
			continue
		default:
		}
		break
	}
	assertNoPushWithin(t, pushes, 100*time.Millisecond)
}

func TestSessionCloseStopsScheduledTasksAndRejectsNewOnes(t *testing.T) {
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		return Response{Text: req.Prompt}, nil
	})
	pushes := make(chan PushResponse, 64)
	session.SetPushSink(func(pr PushResponse) { pushes <- pr })

	if _, err := session.Schedule(ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tick"}); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	waitPush(t, pushes)

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	for {
		select {
		case <-pushes:
			continue
		default:
		}
		break
	}
	assertNoPushWithin(t, pushes, 100*time.Millisecond)

	if _, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "late"}); err == nil {
		t.Fatal("expected Schedule() error after Close")
	}
}

func TestSessionScheduledTaskSkipsTicksWhileBusy(t *testing.T) {
	release := make(chan struct{})
	var runCalls int32
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		atomic.AddInt32(&runCalls, 1)
		if req.Prompt == "user turn" {
			<-release
		}
		return Response{Text: req.Prompt}, nil
	})
	pushes := make(chan PushResponse, 64)
	session.SetPushSink(func(pr PushResponse) { pushes <- pr })

	if _, err := session.Schedule(ScheduledTask{Interval: 100 * time.Millisecond, Prompt: "tick"}); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := session.Send(context.Background(), Request{Prompt: "user turn"}); err != nil {
			t.Errorf("Send() error = %v", err)
		}
	}()

	// ~3 ticker fires happen while the user turn holds the session busy; all
	// must be skipped, not queued behind the mutex.
	time.Sleep(350 * time.Millisecond)
	if got := atomic.LoadInt32(&runCalls); got != 1 {
		t.Fatalf("runCalls while busy = %d, want 1 (ticks must be skipped)", got)
	}

	close(release)
	<-done
	// Immediately after release no queued tick may fire; the next tick is due
	// only at the ticker's next interval boundary.
	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt32(&runCalls); got != 1 {
		t.Fatalf("runCalls right after release = %d, want 1 (no queued ticks)", got)
	}

	waitPush(t, pushes) // ticking resumes afterwards
}

func TestSessionScheduledTaskStopsWhenSessionBreaks(t *testing.T) {
	var runCalls int32
	session := newScheduleTestSession(t, func(_ context.Context, _ Request) (Response, error) {
		atomic.AddInt32(&runCalls, 1)
		return Response{}, errors.New("boom")
	})
	pushes := make(chan PushResponse, 64)
	session.SetPushSink(func(pr PushResponse) { pushes <- pr })

	if _, err := session.Schedule(ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tick"}); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for session.State() != SessionStateBroken {
		if time.Now().After(deadline) {
			t.Fatal("session never became broken")
		}
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)
	calls := atomic.LoadInt32(&runCalls)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&runCalls); got != calls {
		t.Fatalf("runCalls grew after break: %d -> %d", calls, got)
	}
	if tasks := session.Tasks(); len(tasks) != 0 {
		t.Fatalf("Tasks() after break = %#v", tasks)
	}
	assertNoPushWithin(t, pushes, 50*time.Millisecond)
}

func TestSessionScheduleWithoutSinkStillRunsTicks(t *testing.T) {
	var runCalls int32
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		atomic.AddInt32(&runCalls, 1)
		return Response{Text: req.Prompt}, nil
	})

	if _, err := session.Schedule(ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tick"}); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&runCalls) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("ticks never ran without a sink")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSessionScheduleTrimsGeneratedIDPrefix(t *testing.T) {
	session := newScheduleTestSession(t, func(_ context.Context, req Request) (Response, error) {
		return Response{Text: req.Prompt}, nil
	})
	id, err := session.Schedule(ScheduledTask{Interval: time.Minute, Prompt: "a"})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !strings.HasPrefix(id, "task-") {
		t.Fatalf("generated id = %q, want task-<n>", id)
	}
}
