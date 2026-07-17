package agent

import (
	"context"
	"testing"
	"time"
)

// While one bot's session is mid-turn (holding s.mu), a concurrent second Send
// for the same bot (e.g. from a hook or a2a dispatch) enters sessionFor and
// evaluates Matches/State on the busy session. Those must not run while m.mu is
// held, or the whole Manager freezes for every other bot until the turn ends.
func TestManagerSessionForDoesNotFreezeManagerDuringBusyTurn(t *testing.T) {
	driverName := "test-lockorder-" + t.Name()
	inRun := make(chan struct{})
	release := make(chan struct{})
	registerTestDriver(t, driverName, func() Driver {
		return initStubDriver{init: func(_ context.Context, _ Spec) (SessionRuntime, error) {
			return runtimeStub{run: func(_ context.Context, req Request) (Response, error) {
				if req.Prompt == "block" {
					close(inRun)
					<-release
				}
				return Response{Text: req.Prompt}, nil
			}}, nil
		}}
	})

	mgr := NewManager()
	t.Cleanup(func() { close(release); mgr.StopBot("bot-1") })
	spec := Spec{Type: driverName, Command: "codex"}

	// Turn 1 for bot-1 holds s.mu until released.
	go func() { _, _ = mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "block"}) }()
	<-inRun

	// Concurrent second Send for bot-1 enters sessionFor -> Matches on busy session.
	go func() { _, _ = mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "second"}) }()
	time.Sleep(50 * time.Millisecond) // let it reach sessionFor/Matches

	// State(bot-2) needs m.mu; it must stay responsive.
	done := make(chan SessionState, 1)
	go func() { done <- mgr.State("bot-2") }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Manager frozen: m.mu held across a busy-session Matches/State in sessionFor")
	}
}
