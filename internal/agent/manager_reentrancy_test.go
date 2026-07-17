package agent

import (
	"context"
	"testing"
	"time"
)

// Reproduces the production deadlock: the agent calls schedule_task DURING its
// own turn. The turn holds s.mu; scheduling must not need s.mu or it deadlocks.
func TestManagerScheduleFromWithinTurnDoesNotDeadlock(t *testing.T) {
	driverName := "test-reentrancy-schedule-" + t.Name()
	var mgr *Manager
	scheduleErr := make(chan error, 1)
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				if req.Prompt == "user turn" {
					_, err := mgr.Schedule("bot-1", ScheduledTask{Interval: time.Minute, Prompt: "tick"})
					scheduleErr <- err
				}
				return Response{Text: req.Prompt}, nil
			}}, nil
		}}
	})

	mgr = NewManager()
	t.Cleanup(func() { mgr.StopBot("bot-1") })
	spec := Spec{Type: driverName, Command: "codex"}

	done := make(chan struct{})
	go func() {
		_, _ = mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "user turn"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("DEADLOCK: Send with a mid-turn Schedule call did not complete")
	}
	if err := <-scheduleErr; err != nil {
		t.Fatalf("Schedule from within turn: %v", err)
	}
}
