package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerSchedulePushesThroughRegisteredSink(t *testing.T) {
	driverName := "test-manager-schedule-driver-" + t.Name()
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: "cpu: 12%"}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	pushes := make(chan PushResponse, 16)
	// Sink registered before any session exists: Schedule must create the
	// session on demand and inject the sink.
	mgr.SetPushSink("bot-1", func(pr PushResponse) { pushes <- pr })

	spec := Spec{Type: driverName, Command: "codex"}
	taskID, err := mgr.Schedule(context.Background(), "bot-1", spec, ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "check cpu"})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	t.Cleanup(func() { mgr.StopBot("bot-1") })

	pr := waitPush(t, pushes)
	if pr.TaskID != taskID {
		t.Fatalf("push TaskID = %q, want %q", pr.TaskID, taskID)
	}
	if pr.Text != "cpu: 12%" {
		t.Fatalf("push Text = %q", pr.Text)
	}
}

func TestManagerSinkSurvivesSessionRecreation(t *testing.T) {
	driverName := "test-manager-sink-recreate-driver-" + t.Name()
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, spec Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: spec.Command + ":" + req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	pushes := make(chan PushResponse, 64)
	mgr.SetPushSink("bot-1", func(pr PushResponse) { pushes <- pr })
	t.Cleanup(func() { mgr.StopBot("bot-1") })

	alpha := Spec{Type: driverName, Command: "alpha"}
	if _, err := mgr.Schedule(context.Background(), "bot-1", alpha, ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tick"}); err != nil {
		t.Fatalf("Schedule(alpha) error = %v", err)
	}
	waitPush(t, pushes)

	// Spec change replaces the session; the old session's tasks die with it.
	beta := Spec{Type: driverName, Command: "beta"}
	if _, err := mgr.Send(context.Background(), "bot-1", beta, Request{Prompt: "hello"}); err != nil {
		t.Fatalf("Send(beta) error = %v", err)
	}

	// The replacement session must inherit the sink without re-registration.
	if _, err := mgr.Schedule(context.Background(), "bot-1", beta, ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tock"}); err != nil {
		t.Fatalf("Schedule(beta) error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		pr := waitPush(t, pushes)
		if pr.Text == "beta:tock" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw beta push, last = %#v", pr)
		}
	}
}

func TestManagerStopBotClosesSessionAndStopsTasks(t *testing.T) {
	driverName := "test-manager-stopbot-driver-" + t.Name()
	var closeCalls int32
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{
				run: func(_ context.Context, req Request) (Response, error) {
					return Response{Text: req.Prompt}, nil
				},
				close: func() error {
					atomic.AddInt32(&closeCalls, 1)
					return nil
				},
			}, nil
		}}
	})

	mgr := NewManager()
	pushes := make(chan PushResponse, 64)
	mgr.SetPushSink("bot-1", func(pr PushResponse) { pushes <- pr })

	spec := Spec{Type: driverName, Command: "codex"}
	taskID, err := mgr.Schedule(context.Background(), "bot-1", spec, ScheduledTask{Interval: 10 * time.Millisecond, Prompt: "tick"})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	waitPush(t, pushes)

	mgr.StopBot("bot-1")

	if got := atomic.LoadInt32(&closeCalls); got != 1 {
		t.Fatalf("closeCalls = %d", got)
	}
	if got := mgr.State("bot-1"); got != SessionStateStopped {
		t.Fatalf("State() after StopBot = %q", got)
	}
	if mgr.CancelTask("bot-1", taskID) {
		t.Fatal("CancelTask() after StopBot = true")
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

	// StopBot for an unknown bot is a no-op.
	mgr.StopBot("bot-unknown")
}

func TestManagerCancelTask(t *testing.T) {
	driverName := "test-manager-cancel-driver-" + t.Name()
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	t.Cleanup(func() { mgr.StopBot("bot-1") })
	spec := Spec{Type: driverName, Command: "codex"}
	taskID, err := mgr.Schedule(context.Background(), "bot-1", spec, ScheduledTask{ID: "cpu-watch", Interval: time.Minute, Prompt: "tick"})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if taskID != "cpu-watch" {
		t.Fatalf("taskID = %q", taskID)
	}

	if !mgr.CancelTask("bot-1", "cpu-watch") {
		t.Fatal("CancelTask() = false")
	}
	if mgr.CancelTask("bot-1", "cpu-watch") {
		t.Fatal("second CancelTask() = true")
	}
	if mgr.CancelTask("bot-unknown", "cpu-watch") {
		t.Fatal("CancelTask(unknown bot) = true")
	}
}

func TestManagerTasksListsSessionTasks(t *testing.T) {
	driverName := "test-manager-tasks-driver-" + t.Name()
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				return Response{Text: req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	t.Cleanup(func() { mgr.StopBot("bot-1") })
	if got := mgr.Tasks("bot-1"); len(got) != 0 {
		t.Fatalf("Tasks() for unknown bot = %#v", got)
	}

	spec := Spec{Type: driverName, Command: "codex"}
	if _, err := mgr.Schedule(context.Background(), "bot-1", spec, ScheduledTask{ID: "cpu-watch", Interval: time.Minute, Prompt: "tick"}); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	tasks := mgr.Tasks("bot-1")
	if len(tasks) != 1 || tasks[0].ID != "cpu-watch" {
		t.Fatalf("Tasks() = %#v", tasks)
	}
}
