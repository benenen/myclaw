package bot

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

// Results are delivered in the order the turns run (serial Send = FIFO), each to
// its own reply target.
func TestDeliverPreservesFIFOOrderPerBot(t *testing.T) {
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		return agent.Response{Text: req.Prompt}, nil
	}}
	replies := &recordingReplyGateway{}
	o := NewBotMessageOrchestrator(exec, replies, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", QueueSize: 4}, nil
	}}, nil)

	for i, id := range []string{"m1", "m2", "m3"} {
		o.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: id, From: id, Text: id})
		waitForReplyCount(t, replies, i+1) // wait for each reply before sending the next
	}

	if got := replies.texts(); !reflect.DeepEqual(got, []string{"m1", "m2", "m3"}) {
		t.Fatalf("texts = %#v", got)
	}
	if got := replies.targets(); !reflect.DeepEqual(got, []string{"m1", "m2", "m3"}) {
		t.Fatalf("targets = %#v", got)
	}
}

// While a turn is in flight, a second message is accepted (not busy-rejected)
// and delivered after the first completes.
func TestAcceptWhileBusyPipelinesSecondMessage(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "m1" {
			close(started)
			<-release
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	replies := &recordingReplyGateway{}
	o := NewBotMessageOrchestrator(exec, replies, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", QueueSize: 4}, nil
	}}, nil)

	o.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	// m1 is blocked in Send. m2 must be accepted, not rejected with busyReply.
	o.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u2", Text: "two"})

	// Nothing delivered yet (m1 still blocked), and no busy reply.
	for _, txt := range replies.texts() {
		if txt == busyReply {
			t.Fatalf("unexpected busy reply while pipelining: %#v", replies.texts())
		}
	}

	close(release)
	waitForReplyCount(t, replies, 2)
	if got := replies.texts(); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("texts = %#v", got)
	}
}

// StopBot tears down the bot: an in-flight Send is cancelled, its context is
// done, state is removed, and no reply is sent for the interrupted turn.
func TestStopBotCancelsInFlightAndRemovesState(t *testing.T) {
	started := make(chan struct{})
	sawDone := make(chan struct{})
	var once sync.Once
	exec := &fakeExecutor{send: func(ctx context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		once.Do(func() { close(started) })
		<-ctx.Done() // block until StopBot cancels us
		close(sawDone)
		return agent.Response{}, ctx.Err()
	}}
	var mu sync.Mutex
	var replies []string
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		mu.Lock()
		replies = append(replies, resp.Text)
		mu.Unlock()
		return nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{}, nil)

	o.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started

	o.StopBot("bot-1")

	select {
	case <-sawDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight Send was not cancelled by StopBot")
	}

	// State is removed.
	if o.HasBotState("bot-1") {
		t.Fatal("expected bot state removed after StopBot")
	}
	// No reply for the interrupted turn.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(replies) != 0 {
		t.Fatalf("expected no reply on StopBot, got %#v", replies)
	}
}

// StopBot on an in-flight orchestrator turn must finalize its progress session
// (Fail), otherwise the Feishu flush goroutine leaks and the trace card sticks.
func TestStopBotFinalizesInFlightProgressSession(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	exec := &fakeExecutor{send: func(ctx context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return agent.Response{}, ctx.Err()
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	resolver := fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Orchestrator: true, Timeout: time.Minute, QueueSize: 1}, nil
	}}
	sess := &fakeProgressSession{}
	o := NewBotMessageOrchestrator(exec, gateway, resolver, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), InboundMessage{BotID: "brain_1", MessageID: "m1", From: "u1", Text: "go"})
	<-started

	o.StopBot("brain_1")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sess.counts().failed == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected StopBot to Fail the in-flight session, counts = %+v", sess.counts())
}
