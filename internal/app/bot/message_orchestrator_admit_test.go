package bot

import (
	"testing"
)

// A message with no MessageID must be enqueued exactly once. The old code
// looped, enqueuing the same message until the queue filled, so one message
// was processed cap(queue) times.
func TestAdmitMessageEmptyIDEnqueuesOnce(t *testing.T) {
	o := NewBotMessageOrchestrator(&fakeExecutor{}, &recordingReplyGateway{}, fakeResolver{}, nil)

	// Install a bot state with a buffered worker queue and no drainer, so
	// whatever admitMessage enqueues stays countable.
	o.mu.Lock()
	o.bots["bot-1"] = &botState{
		worker:    &botWorker{queue: make(chan InboundMessage, 4)},
		queueSize: 4,
	}
	o.mu.Unlock()

	state, admitted, duplicate := o.admitMessage(InboundMessage{BotID: "bot-1", Text: "hi"})
	if state == nil || !admitted || duplicate {
		t.Fatalf("admitMessage = (state=%v, admitted=%v, duplicate=%v)", state != nil, admitted, duplicate)
	}

	o.mu.Lock()
	queued := len(o.bots["bot-1"].worker.queue)
	o.mu.Unlock()
	if queued != 1 {
		t.Fatalf("empty-id message enqueued %d times, want 1", queued)
	}
}
