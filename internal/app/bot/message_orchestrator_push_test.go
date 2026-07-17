package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

// Push/StopBot recording for fakeExecutor (methods live here to keep the
// scheduling concerns together; the type is in message_orchestrator_test.go).
type executorPushState struct {
	mu      sync.Mutex
	sinks   map[string]agent.PushSink
	stopped []string
}

func (m *fakeExecutor) SetPushSink(botID string, sink agent.PushSink) {
	m.push.mu.Lock()
	defer m.push.mu.Unlock()
	if m.push.sinks == nil {
		m.push.sinks = make(map[string]agent.PushSink)
	}
	m.push.sinks[botID] = sink
}

func (m *fakeExecutor) StopBot(botID string) {
	m.push.mu.Lock()
	defer m.push.mu.Unlock()
	m.push.stopped = append(m.push.stopped, botID)
}

func (m *fakeExecutor) sink(botID string) agent.PushSink {
	m.push.mu.Lock()
	defer m.push.mu.Unlock()
	return m.push.sinks[botID]
}

func (m *fakeExecutor) stoppedBots() []string {
	m.push.mu.Lock()
	defer m.push.mu.Unlock()
	return append([]string(nil), m.push.stopped...)
}

func TestPushResponseDeliveredToLastReplyTarget(t *testing.T) {
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{Text: "turn answer"}, nil
	}}
	replies := &recordingReplyGateway{}
	o := NewBotMessageOrchestrator(exec, replies, fakeResolver{}, nil)

	o.HandleMessage(context.Background(), InboundMessage{
		BotID:     "bot-1",
		MessageID: "m1",
		From:      "user_1",
		Text:      "watch cpu",
	})
	waitForReplyCount(t, replies, 1)

	sink := exec.sink("bot-1")
	if sink == nil {
		t.Fatal("expected push sink registered for bot-1")
	}
	sink(agent.PushResponse{Response: agent.Response{Text: "cpu: 12%"}, TaskID: "task-1"})

	waitForReplyCount(t, replies, 2)
	texts := replies.texts()
	if texts[len(texts)-1] != "cpu: 12%" {
		t.Fatalf("last reply = %q", texts[len(texts)-1])
	}
	targets := replies.targets()
	if targets[len(targets)-1] != "user_1" {
		t.Fatalf("push went to %q, want user_1", targets[len(targets)-1])
	}
}

func TestPushResponseDroppedWithoutReplyTarget(t *testing.T) {
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{Text: "unused"}, nil
	}}
	replies := &recordingReplyGateway{}
	o := NewBotMessageOrchestrator(exec, replies, fakeResolver{}, nil)

	// No message was ever handled for this bot: there is no reply target, so
	// the push must be dropped without a reply (and without panicking).
	o.deliverPush("bot-unknown", agent.PushResponse{Response: agent.Response{Text: "cpu: 12%"}, TaskID: "task-1"})

	time.Sleep(50 * time.Millisecond)
	if got := replies.texts(); len(got) != 0 {
		t.Fatalf("replies = %v, want none", got)
	}
}

func TestStopBotStopsExecutorSessions(t *testing.T) {
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{Text: "turn answer"}, nil
	}}
	replies := &recordingReplyGateway{}
	o := NewBotMessageOrchestrator(exec, replies, fakeResolver{}, nil)

	o.HandleMessage(context.Background(), InboundMessage{
		BotID:     "bot-1",
		MessageID: "m1",
		From:      "user_1",
		Text:      "hello",
	})
	waitForReplyCount(t, replies, 1)

	o.StopBot("bot-1")
	if got := exec.stoppedBots(); len(got) != 1 || got[0] != "bot-1" {
		t.Fatalf("executor stopped bots = %v, want [bot-1]", got)
	}

	// StopBot for an unknown bot must not reach the executor.
	o.StopBot("bot-unknown")
	if got := exec.stoppedBots(); len(got) != 1 {
		t.Fatalf("executor stopped bots after unknown StopBot = %v", got)
	}
}
