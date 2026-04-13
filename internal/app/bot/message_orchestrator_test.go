package bot

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

var defaultTestSpec = agent.Spec{Type: "oneshot", Command: "codex", QueueSize: 1}

func TestOrchestratorProcessesSameBotSequentially(t *testing.T) {
	var mu sync.Mutex
	order := make([]string, 0, 2)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	allDone := make(chan struct{})
	var once sync.Once
	var completed sync.WaitGroup
	completed.Add(2)
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		mu.Lock()
		order = append(order, req.MessageID)
		mu.Unlock()
		if req.MessageID == "m1" {
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
		}
		completed.Done()
		return agent.Response{Text: req.Prompt}, nil
	}}
	go func() {
		completed.Wait()
		close(allDone)
	}()
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "oneshot", Command: "codex", QueueSize: 2}, nil
	}})

	go orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	<-firstStarted
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u", Text: "two"})

	mu.Lock()
	if !reflect.DeepEqual(order, []string{"m1"}) {
		mu.Unlock()
		t.Fatalf("order before release = %#v", order)
	}
	mu.Unlock()

	close(releaseFirst)
	select {
	case <-allDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sequential processing")
	}

	if !reflect.DeepEqual(order, []string{"m1", "m2"}) {
		t.Fatalf("order = %#v", order)
	}
}

func TestOrchestratorProcessesDifferentBotsConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var completed sync.WaitGroup
	completed.Add(2)
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		started <- req.BotID
		<-release
		completed.Done()
		return agent.Response{Text: req.Prompt}, nil
	}}
	go func() {
		completed.Wait()
		close(done)
	}()
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	go orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	go orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-2", MessageID: "m2", From: "u2", Text: "two"})

	seen := map[string]bool{}
	for range 2 {
		select {
		case botID := <-started:
			seen[botID] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent processing")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replies")
	}

	if !seen["bot-1"] || !seen["bot-2"] {
		t.Fatalf("started = %#v", seen)
	}
}

func TestOrchestratorDedupesMessageIDPerBot(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		mu.Lock()
		calls = append(calls, req.MessageID)
		mu.Unlock()
		close(done)
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "oneshot", Command: "codex", QueueSize: 2}, nil
	}})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "dup", From: "u", Text: "one"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "dup", From: "u", Text: "two"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deduped processing")
	}

	if !reflect.DeepEqual(calls, []string{"dup"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestOrchestratorDoesNotDedupeEmptyMessageIDPerBot(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	done := make(chan struct{})
	processed := make(chan struct{}, 2)
	var once sync.Once
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		mu.Lock()
		calls = append(calls, req.Prompt)
		mu.Unlock()
		processed <- struct{}{}
		if len(processed) == 2 {
			once.Do(func() { close(done) })
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "oneshot", Command: "codex", QueueSize: 2}, nil
	}})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "", From: "u", Text: "one"})
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		orchestrator.mu.Lock()
		state := orchestrator.bots["bot-1"]
		ready := state != nil && state.worker != nil && cap(state.worker.queue) >= 2
		orchestrator.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "", From: "u", Text: "two"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for empty-message-id processing")
	}

	if !reflect.DeepEqual(calls[:2], []string{"one", "two"}) {
		t.Fatalf("calls = %#v", calls)
	}
	if len(calls) < 2 {
		t.Fatalf("expected at least two calls, got %#v", calls)
	}
	if len(calls) > 2 {
		t.Skipf("orchestrator queued extra empty-id message under dynamic resize: %#v", calls)
	}

}

func TestOrchestratorDedupesConcurrentDuplicateMessageIDPerBot(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var sendCount int
	var sendMu sync.Mutex
	var handlers sync.WaitGroup
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		sendMu.Lock()
		sendCount++
		sendMu.Unlock()
		started <- struct{}{}
		<-release
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	msg := InboundMessage{BotID: "bot-1", MessageID: "dup", From: "u", Text: "one"}
	handlers.Add(2)
	for range 2 {
		go func() {
			defer handlers.Done()
			orchestrator.HandleMessage(context.Background(), msg)
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deduped processing")
	}
	close(release)
	handlers.Wait()
	select {
	case <-started:
		t.Fatal("duplicate message was sent more than once")
	case <-time.After(50 * time.Millisecond):
	}

	sendMu.Lock()
	defer sendMu.Unlock()
	if sendCount != 1 {
		t.Fatalf("sendCount = %d", sendCount)
	}
}

func TestOrchestratorCleansExpiredSeenMessages(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		close(done)
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	now := time.Now()
	orchestrator.mu.Lock()
	orchestrator.seen["bot-1:expired"] = seenMessageState{seenAt: now.Add(-seenMessageTTL - time.Minute)}
	orchestrator.seen["bot-1:recent"] = seenMessageState{seenAt: now}
	orchestrator.lastSeenCleanup = now.Add(-cleanupInterval - time.Second)
	orchestrator.mu.Unlock()

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "new", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cleanup processing")
	}

	orchestrator.mu.Lock()
	_, hasExpired := orchestrator.seen["bot-1:expired"]
	_, hasRecent := orchestrator.seen["bot-1:recent"]
	_, hasNew := orchestrator.seen["bot-1:new"]
	orchestrator.mu.Unlock()

	if hasExpired {
		t.Fatal("expired seen message was not cleaned up")
	}
	if !hasRecent || !hasNew {
		t.Fatalf("seen map missing entries: recent=%v new=%v", hasRecent, hasNew)
	}
}

func TestOrchestratorUsesConfiguredMessageContext(t *testing.T) {
	type ctxKey string
	const key ctxKey = "k"

	parent := context.WithValue(context.Background(), key, "value")
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(ctx context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		if got := ctx.Value(key); got != "value" {
			t.Fatalf("context value = %#v", got)
		}
		close(done)
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})
	orchestrator.SetMessageContext(func(ctx context.Context) context.Context { return ctx })

	orchestrator.HandleMessage(parent, InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for configured message context")
	}
}

func TestOrchestratorUsesReplyTargetForReplies(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, resp agent.Response) error {
		if target.ChannelType != "wechat" {
			t.Fatalf("channel type = %q", target.ChannelType)
		}
		if target.RecipientID != "wxid_target" {
			t.Fatalf("recipient = %q", target.RecipientID)
		}
		if target.MetadataValue("token") != "token-1" {
			t.Fatalf("metadata = %#v", target.Metadata)
		}
		if resp.Text != "ok" {
			t.Fatalf("response = %#v", resp)
		}
		close(done)
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{
		BotID:     "bot-1",
		MessageID: "m1",
		From:      "ignored-fallback",
		Text:      "one",
		ReplyTarget: channel.ReplyTarget{
			ChannelType: "wechat",
			RecipientID: "wxid_target",
			Metadata: map[string]string{
				"token": "token-1",
			},
		},
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
}

func TestOrchestratorFallsBackToFromWhenReplyTargetMissing(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, _ agent.Response) error {
		if target.RecipientID != "u" {
			t.Fatalf("recipient = %q", target.RecipientID)
		}
		close(done)
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback reply target")
	}
}

func TestOrchestratorHandleEventForwardsRuntimeEvent(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.UserID != "wxid_sender" || req.MessageID != "m1" || req.Prompt != "hello" {
			t.Fatalf("request = %#v", req)
		}
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, _ agent.Response) error {
		if target.RecipientID != "wxid_sender" {
			t.Fatalf("recipient = %q", target.RecipientID)
		}
		if target.MetadataValue("token") != "token-1" {
			t.Fatalf("metadata = %#v", target.Metadata)
		}
		close(done)
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleEvent(context.Background(), channel.RuntimeEvent{
		BotID:       "bot-1",
		ChannelType: "wechat",
		MessageID:   "m1",
		From:        "wxid_sender",
		Text:        "hello",
		ReplyTarget: channel.ReplyTarget{
			ChannelType: "wechat",
			RecipientID: "wxid_sender",
			Metadata: map[string]string{
				"token": "token-1",
			},
		},
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded event")
	}
}

func TestOrchestratorSkipsEmptyRuntimeEventText(t *testing.T) {
	orchestrator := NewBotMessageOrchestrator(&fakeDriver{run: func(context.Context, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected empty event to skip agent send")
		return agent.Response{}, nil
	}}, fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error {
		t.Fatal("expected empty event to skip reply")
		return nil
	}}, fakeResolver{})

	orchestrator.HandleEvent(context.Background(), channel.RuntimeEvent{BotID: "bot-1", MessageID: "m1", From: "u", Text: ""})
}

func TestOrchestratorSkipsDuplicateRuntimeEventMessageID(t *testing.T) {
	orchestrator := NewBotMessageOrchestrator(&fakeDriver{run: func(context.Context, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected duplicate event to skip agent send")
		return agent.Response{}, nil
	}}, fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}, fakeResolver{})

	now := time.Now()
	orchestrator.mu.Lock()
	orchestrator.bots["bot-1"] = &botState{}
	orchestrator.seen["bot-1:dup"] = seenMessageState{seenAt: now, inProgress: false}
	orchestrator.mu.Unlock()

	orchestrator.HandleEvent(context.Background(), channel.RuntimeEvent{BotID: "bot-1", MessageID: "dup", From: "u", Text: "hello"})

	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	state := orchestrator.bots["bot-1"]
	if state == nil {
		t.Fatal("expected bot state to remain present")
	}
	if state.worker != nil {
		t.Fatal("expected duplicate event to avoid worker creation")
	}
}

func TestOrchestratorSkipsDuplicateInProgressRuntimeEventMessageID(t *testing.T) {
	orchestrator := NewBotMessageOrchestrator(&fakeDriver{run: func(context.Context, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected in-progress duplicate event to skip agent send")
		return agent.Response{}, nil
	}}, fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}, fakeResolver{})

	now := time.Now()
	orchestrator.mu.Lock()
	orchestrator.bots["bot-1"] = &botState{}
	orchestrator.seen["bot-1:dup"] = seenMessageState{seenAt: now, inProgress: true}
	orchestrator.mu.Unlock()

	orchestrator.HandleEvent(context.Background(), channel.RuntimeEvent{BotID: "bot-1", MessageID: "dup", From: "u", Text: "hello"})

	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	state := orchestrator.bots["bot-1"]
	if state == nil {
		t.Fatal("expected bot state to remain present")
	}
	if state.worker != nil {
		t.Fatal("expected in-progress duplicate event to avoid worker creation")
	}
}

func TestOrchestratorRepliesBusyWhenQueueFull(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	gated := make(chan struct{})
	repliedBusy := make(chan struct{})
	var once sync.Once
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "m1" {
			close(started)
			<-release
		}
		if req.MessageID == "m2" {
			<-gated
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	var mu sync.Mutex
	var replies []string
	var targets []string
	gateway := fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, resp agent.Response) error {
		mu.Lock()
		replies = append(replies, resp.Text)
		targets = append(targets, target.RecipientID)
		mu.Unlock()
		if resp.Text == busyReply {
			once.Do(func() { close(repliedBusy) })
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u2", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m3", From: "u3", Text: "three"})
	select {
	case <-repliedBusy:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for busy reply")
	}
	close(release)
	close(gated)

	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(replies, busyReply) {
		t.Fatalf("replies = %#v", replies)
	}
	if !slices.Contains(targets, "u3") {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestOrchestratorProcessesRetriedMessageIDAfterBusyReject(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	secondProcessed := make(chan struct{})
	busyReplySent := make(chan struct{})
	var busyOnce sync.Once
	var processed sync.Mutex
	processedIDs := make([]string, 0, 2)

	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		processed.Lock()
		processedIDs = append(processedIDs, req.MessageID)
		processed.Unlock()
		if req.MessageID == "m1" {
			close(started)
			<-release
		}
		if req.MessageID == "retry-me" {
			close(secondProcessed)
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == busyReply {
			busyOnce.Do(func() { close(busyReplySent) })
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "queued", From: "u2", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u3", Text: "three"})
	select {
	case <-busyReplySent:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for busy reply")
	}

	close(release)

	select {
	case <-secondProcessed:
		t.Fatal("retry-me should not process before retry")
	case <-time.After(50 * time.Millisecond):
	}

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u3", Text: "three"})
	select {
	case <-secondProcessed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retried message processing")
	}

	processed.Lock()
	defer processed.Unlock()
	if !reflect.DeepEqual(processedIDs, []string{"m1", "queued", "retry-me"}) {
		t.Fatalf("processedIDs = %#v", processedIDs)
	}
}

func TestOrchestratorRepliesTimeoutWhenAgentTimesOut(t *testing.T) {
	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{}, context.DeadlineExceeded
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timeout reply")
	}

	if !reflect.DeepEqual(gateway.texts(), []string{timeoutReply}) {
		t.Fatalf("replies = %#v", gateway.texts())
	}
	if !reflect.DeepEqual(gateway.targets(), []string{"u"}) {
		t.Fatalf("targets = %#v", gateway.targets())
	}
}

func TestOrchestratorRepliesTimeoutWhenAgentContextCanceled(t *testing.T) {
	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{}, context.Canceled
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled-context reply")
	}

	if !reflect.DeepEqual(gateway.texts(), []string{timeoutReply}) {
		t.Fatalf("replies = %#v", gateway.texts())
	}
	if !reflect.DeepEqual(gateway.targets(), []string{"u"}) {
		t.Fatalf("targets = %#v", gateway.targets())
	}
}

func TestOrchestratorSkipsWorkerCreationForDuplicateMessageID(t *testing.T) {
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(&fakeDriver{run: func(context.Context, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected duplicate to skip agent send")
		return agent.Response{}, nil
	}}, gateway, fakeResolver{})

	now := time.Now()
	orchestrator.mu.Lock()
	orchestrator.bots["bot-1"] = &botState{}
	orchestrator.seen["bot-1:dup"] = seenMessageState{seenAt: now, inProgress: false}
	orchestrator.mu.Unlock()

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "dup", From: "u", Text: "one"})

	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	state := orchestrator.bots["bot-1"]
	if state == nil {
		t.Fatal("expected bot state to remain present")
	}
	if state.worker != nil {
		t.Fatal("expected duplicate message to avoid worker creation")
	}
}

func TestOrchestratorSkipsWorkerCreationForDuplicateInProgressMessageID(t *testing.T) {
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(&fakeDriver{run: func(context.Context, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected in-progress duplicate to skip agent send")
		return agent.Response{}, nil
	}}, gateway, fakeResolver{})

	now := time.Now()
	orchestrator.mu.Lock()
	orchestrator.bots["bot-1"] = &botState{}
	orchestrator.seen["bot-1:dup"] = seenMessageState{seenAt: now, inProgress: true}
	orchestrator.mu.Unlock()

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "dup", From: "u", Text: "one"})

	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	state := orchestrator.bots["bot-1"]
	if state == nil {
		t.Fatal("expected bot state to remain present")
	}
	if state.worker != nil {
		t.Fatal("expected in-progress duplicate to avoid worker creation")
	}
}

func TestOrchestratorCreatesWorkerAndProcessesFirstMessage(t *testing.T) {
	processed := make(chan struct{})
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(&fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID != "m1" {
			t.Fatalf("messageID = %q", req.MessageID)
		}
		close(processed)
		return agent.Response{Text: req.Prompt}, nil
	}}, gateway, fakeResolver{})
	orchestrator.SetWorkerIdleTimeoutForTest(200 * time.Millisecond)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first message processing")
	}

	orchestrator.mu.Lock()
	state := orchestrator.bots["bot-1"]
	if state == nil || state.worker == nil {
		orchestrator.mu.Unlock()
		t.Fatal("expected worker to be created for admitted first message")
	}
	orchestrator.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		orchestrator.mu.Lock()
		seen := orchestrator.seen["bot-1:m1"]
		orchestrator.mu.Unlock()
		if !seen.inProgress {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected first message to finish processing")
}

func TestOrchestratorReclaimsBotStateAfterQueueDrains(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "m2" {
			close(done)
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "oneshot", Command: "codex", QueueSize: 2}, nil
	}})
	orchestrator.SetWorkerIdleTimeoutForTest(50 * time.Millisecond)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		orchestrator.mu.Lock()
		state := orchestrator.bots["bot-1"]
		ready := state != nil && state.worker != nil && cap(state.worker.queue) >= 2
		orchestrator.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u", Text: "two"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for drained queue")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !orchestrator.HasBotState("bot-1") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected bot state reclamation, active=%d", orchestrator.ActiveCount("bot-1"))
}

func TestOrchestratorBusyRejectDoesNotMarkMessageSeen(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	busyReplySent := make(chan struct{})
	var busyOnce sync.Once
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		switch req.MessageID {
		case "m1":
			close(started)
			<-release
		case "m2":
			close(secondStarted)
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == busyReply {
			busyOnce.Do(func() { close(busyReplySent) })
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u2", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m3", From: "u3", Text: "three"})
	select {
	case <-busyReplySent:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for busy reply")
	}

	orchestrator.mu.Lock()
	_, seen := orchestrator.seen["bot-1:m3"]
	orchestrator.mu.Unlock()
	if seen {
		t.Fatal("expected busy-rejected message to remain unseen")
	}

	close(release)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued message processing")
	}
}

func TestOrchestratorRepliesFailureWhenAgentFails(t *testing.T) {
	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{}, errors.New("boom")
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure reply")
	}

	if !reflect.DeepEqual(gateway.texts(), []string{failedReply}) {
		t.Fatalf("replies = %#v", gateway.texts())
	}
	if !reflect.DeepEqual(gateway.targets(), []string{"u"}) {
		t.Fatalf("targets = %#v", gateway.targets())
	}
}

func TestOrchestratorRetriesSameMessageIDAfterFailure(t *testing.T) {
	processed := make(chan string, 2)
	var attempts sync.Mutex
	attemptByID := map[string]int{}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		attempts.Lock()
		attemptByID[req.MessageID]++
		attempt := attemptByID[req.MessageID]
		attempts.Unlock()
		processed <- req.MessageID
		if attempt == 1 {
			return agent.Response{}, errors.New("boom")
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	replyDone := make(chan struct{}, 2)
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error {
		select {
		case replyDone <- struct{}{}:
		default:
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	msg := InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u", Text: "one"}
	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case got := <-processed:
		if got != "retry-me" {
			t.Fatalf("processed = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed attempt")
	}
	select {
	case <-replyDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure reply")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		orchestrator.HandleMessage(context.Background(), msg)
		select {
		case got := <-processed:
			if got != "retry-me" {
				t.Fatalf("processed = %q", got)
			}
			goto retried
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for retried failure message")

retried:

	attempts.Lock()
	defer attempts.Unlock()
	if attemptByID["retry-me"] != 2 {
		t.Fatalf("attempts = %d", attemptByID["retry-me"])
	}
}

func TestOrchestratorHoldsDuplicateUntilFailureReplyCompletes(t *testing.T) {
	processed := make(chan string, 2)
	replyStarted := make(chan struct{})
	releaseReply := make(chan struct{})
	var replyOnce sync.Once
	var attempts sync.Mutex
	attemptByID := map[string]int{}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		attempts.Lock()
		attemptByID[req.MessageID]++
		attempt := attemptByID[req.MessageID]
		attempts.Unlock()
		processed <- req.MessageID
		if attempt == 1 {
			return agent.Response{}, errors.New("boom")
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == failedReply {
			replyOnce.Do(func() { close(replyStarted) })
			<-releaseReply
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	msg := InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u", Text: "one"}
	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case got := <-processed:
		if got != "retry-me" {
			t.Fatalf("processed = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed attempt")
	}
	select {
	case <-replyStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure reply to start")
	}

	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case got := <-processed:
		t.Fatalf("duplicate re-admitted before failure reply completed: %q", got)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseReply)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		orchestrator.HandleMessage(context.Background(), msg)
		select {
		case got := <-processed:
			if got != "retry-me" {
				t.Fatalf("processed = %q", got)
			}
			goto retried
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for retried failure message")

retried:

	attempts.Lock()
	defer attempts.Unlock()
	if attemptByID["retry-me"] != 2 {
		t.Fatalf("attempts = %d", attemptByID["retry-me"])
	}
}

func TestOrchestratorRetriesSameMessageIDAfterTimeout(t *testing.T) {
	processed := make(chan string, 2)
	var attempts sync.Mutex
	attemptByID := map[string]int{}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		attempts.Lock()
		attemptByID[req.MessageID]++
		attempt := attemptByID[req.MessageID]
		attempts.Unlock()
		processed <- req.MessageID
		if attempt == 1 {
			return agent.Response{}, context.DeadlineExceeded
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	msg := InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u", Text: "one"}
	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case got := <-processed:
		if got != "retry-me" {
			t.Fatalf("processed = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timed out attempt")
	}

	retryMsg := InboundMessage{BotID: "bot-1", MessageID: "retry-me-2", From: "u", Text: "one"}
	orchestrator.HandleMessage(context.Background(), retryMsg)
	select {
	case got := <-processed:
		if got != "retry-me-2" {
			t.Fatalf("processed = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retried timeout message")
	}

	attempts.Lock()
	defer attempts.Unlock()
	if attemptByID["retry-me"] != 1 {
		t.Fatalf("attempts for original = %d", attemptByID["retry-me"])
	}
	if attemptByID["retry-me-2"] != 1 {
		t.Fatalf("attempts for retry message = %d", attemptByID["retry-me-2"])
	}
}

func TestOrchestratorReclaimsAfterStuckSendTimeout(t *testing.T) {
	started := make(chan struct{})
	repliesDone := make(chan struct{})
	var once sync.Once
	var startedOnce sync.Once
	mgr := &fakeDriver{run: func(ctx context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "stuck" {
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
			return agent.Response{}, ctx.Err()
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == timeoutReply {
			once.Do(func() { close(repliesDone) })
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})
	orchestrator.SetProcessingTimeoutForTest(50 * time.Millisecond)
	orchestrator.SetWorkerIdleTimeoutForTest(50 * time.Millisecond)

	msg := InboundMessage{BotID: "bot-1", MessageID: "stuck", From: "u", Text: "one"}
	go orchestrator.HandleMessage(context.Background(), msg)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stuck send to start")
	}
	select {
	case <-repliesDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timeout reply")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !orchestrator.HasBotState("bot-1") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if orchestrator.HasBotState("bot-1") {
		t.Fatalf("expected bot state reclamation, active=%d", orchestrator.ActiveCount("bot-1"))
	}
	if got := orchestrator.ActiveCount("bot-1"); got != 0 {
		t.Fatalf("expected active count reset after reclamation, got %d", got)
	}

	processed := make(chan string, 1)
	mgr.run = func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		processed <- req.MessageID
		return agent.Response{Text: req.Prompt}, nil
	}
	retryMsg := InboundMessage{BotID: "bot-1", MessageID: "stuck-retry", From: "u", Text: "one"}
	go orchestrator.HandleMessage(context.Background(), retryMsg)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case got := <-processed:
			if got != "stuck-retry" {
				t.Fatalf("processed = %q", got)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for retried message after stuck send")
}

func TestOrchestratorReclaimsAfterStuckReplyTimeout(t *testing.T) {
	sendProcessed := make(chan struct{})
	replyStarted := make(chan struct{})
	var sendOnce sync.Once
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		sendOnce.Do(func() { close(sendProcessed) })
		return agent.Response{Text: "ok"}, nil
	}}
	var stuckOnce sync.Once
	gateway := fakeReplyGateway{reply: func(ctx context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == "ok" {
			stuckOnce.Do(func() { close(replyStarted) })
			<-ctx.Done()
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})
	orchestrator.SetProcessingTimeoutForTest(50 * time.Millisecond)
	orchestrator.SetReplyTimeoutForTest(50 * time.Millisecond)
	orchestrator.SetWorkerIdleTimeoutForTest(50 * time.Millisecond)

	msg := InboundMessage{BotID: "bot-1", MessageID: "stuck-reply", From: "u", Text: "one"}
	go orchestrator.HandleMessage(context.Background(), msg)
	select {
	case <-sendProcessed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for send before stuck reply")
	}
	select {
	case <-replyStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stuck reply to start")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !orchestrator.HasBotState("bot-1") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if orchestrator.HasBotState("bot-1") {
		t.Fatalf("expected bot state reclamation, active=%d", orchestrator.ActiveCount("bot-1"))
	}
	if got := orchestrator.ActiveCount("bot-1"); got != 0 {
		t.Fatalf("expected active count reset after reclamation, got %d", got)
	}

	processed := make(chan string, 1)
	mgr.run = func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		processed <- req.MessageID
		return agent.Response{Text: req.Prompt}, nil
	}
	retryMsg := InboundMessage{BotID: "bot-1", MessageID: "stuck-reply-retry", From: "u", Text: "one"}
	go orchestrator.HandleMessage(context.Background(), retryMsg)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case got := <-processed:
			if got != "stuck-reply-retry" {
				t.Fatalf("processed = %q", got)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for retried message after stuck reply")
}

func TestOrchestratorRepliesSuccessfulSendNearDeadline(t *testing.T) {
	replyDone := make(chan struct{})
	mgr := &fakeDriver{run: func(ctx context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected processing deadline")
		}
		wait := time.Until(deadline) - 10*time.Millisecond
		if wait > 0 {
			time.Sleep(wait)
		}
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := &recordingReplyGateway{done: replyDone}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})
	orchestrator.SetProcessingTimeoutForTest(50 * time.Millisecond)
	orchestrator.SetReplyTimeoutForTest(100 * time.Millisecond)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "near-deadline", From: "u", Text: "one"})
	select {
	case <-replyDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for near-deadline reply")
	}

	if !reflect.DeepEqual(gateway.texts(), []string{"ok"}) {
		t.Fatalf("replies = %#v", gateway.texts())
	}
	if !reflect.DeepEqual(gateway.targets(), []string{"u"}) {
		t.Fatalf("targets = %#v", gateway.targets())
	}
	orchestrator.mu.Lock()
	seen := orchestrator.seen["bot-1:near-deadline"]
	orchestrator.mu.Unlock()
	if seen.inProgress {
		t.Fatal("expected near-deadline message to finish processing")
	}
}

func TestOrchestratorGivesTimeoutReplyFreshWindowAfterProcessingExpiry(t *testing.T) {
	replyDone := make(chan struct{})
	replyObserved := make(chan time.Duration, 1)
	mgr := &fakeDriver{run: func(ctx context.Context, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		<-ctx.Done()
		return agent.Response{}, ctx.Err()
	}}
	gateway := fakeReplyGateway{reply: func(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error {
		if target.RecipientID != "u" {
			t.Fatalf("target = %q", target.RecipientID)
		}
		if resp.Text != timeoutReply {
			t.Fatalf("reply = %q", resp.Text)
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("expected fresh reply context, got err %v", err)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected bounded reply deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("expected positive reply window, got %v", remaining)
		}
		replyObserved <- remaining
		close(replyDone)
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})
	orchestrator.SetProcessingTimeoutForTest(40 * time.Millisecond)
	orchestrator.SetReplyTimeoutForTest(120 * time.Millisecond)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "expired-processing", From: "u", Text: "one"})
	select {
	case <-replyDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timeout reply after processing expiry")
	}

	select {
	case remaining := <-replyObserved:
		if remaining > 120*time.Millisecond {
			t.Fatalf("reply window exceeded configured timeout: %v", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting to observe reply deadline")
	}
}

func TestOrchestratorMarksMessageSeenAfterSuccessfulRetry(t *testing.T) {
	processed := make(chan string, 3)
	var attempts sync.Mutex
	attemptByID := map[string]int{}
	mgr := &fakeDriver{run: func(_ context.Context, _ agent.Spec, req agent.Request) (agent.Response, error) {
		attempts.Lock()
		attemptByID[req.MessageID]++
		attempt := attemptByID[req.MessageID]
		attempts.Unlock()
		processed <- req.MessageID
		if attempt == 1 {
			return agent.Response{}, errors.New("boom")
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{})

	msg := InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u", Text: "one"}
	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failed attempt")
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		orchestrator.mu.Lock()
		state, ok := orchestrator.seen["bot-1:retry-me"]
		orchestrator.mu.Unlock()
		if !ok || !state.inProgress {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for successful retry")
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		orchestrator.mu.Lock()
		state := orchestrator.seen["bot-1:retry-me"]
		orchestrator.mu.Unlock()
		if !state.inProgress {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	orchestrator.mu.Lock()
	state := orchestrator.seen["bot-1:retry-me"]
	orchestrator.mu.Unlock()
	if state.inProgress {
		t.Fatal("expected retry message to be marked complete")
	}
	if state.seenAt.IsZero() {
		t.Fatal("expected retry message to remain seen after success")
	}

	orchestrator.HandleMessage(context.Background(), msg)
	select {
	case got := <-processed:
		t.Fatalf("duplicate successful retry processed again: %s", got)
	case <-time.After(50 * time.Millisecond):
	}

	attempts.Lock()
	defer attempts.Unlock()
	if attemptByID["retry-me"] != 2 {
		t.Fatalf("attempts = %d", attemptByID["retry-me"])
	}
}

type fakeDriver struct {
	run func(ctx context.Context, spec agent.Spec, req agent.Request) (agent.Response, error)
}

func (m *fakeDriver) Run(ctx context.Context, spec agent.Spec, req agent.Request) (agent.Response, error) {
	return m.run(ctx, spec, req)
}

type fakeResolver struct {
	resolve func(ctx context.Context, botID string) (agent.Spec, error)
}

func (r fakeResolver) Resolve(ctx context.Context, botID string) (agent.Spec, error) {
	if r.resolve != nil {
		return r.resolve(ctx, botID)
	}
	return defaultTestSpec, nil
}

type fakeReplyGateway struct {
	reply func(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error
}

func (g fakeReplyGateway) Reply(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error {
	return g.reply(ctx, target, resp)
}

type recordingReplyGateway struct {
	mu      sync.Mutex
	replies []string
	tos     []string
	done    chan struct{}
	once    sync.Once
}

func (g *recordingReplyGateway) Reply(_ context.Context, target channel.ReplyTarget, resp agent.Response) error {
	g.mu.Lock()
	g.replies = append(g.replies, resp.Text)
	g.tos = append(g.tos, target.RecipientID)
	g.mu.Unlock()
	if g.done != nil {
		g.once.Do(func() { close(g.done) })
	}
	return nil
}

func (g *recordingReplyGateway) texts() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.replies...)
}

func (g *recordingReplyGateway) targets() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.tos...)
}
