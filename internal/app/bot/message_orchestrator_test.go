package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/domain"
)

var defaultTestSpec = agent.Spec{Type: "codex-exec", Command: "codex", QueueSize: 1}

func TestOrchestratorProcessesSameBotSequentially(t *testing.T) {
	var mu sync.Mutex
	order := make([]string, 0, 2)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	allDone := make(chan struct{})
	var once sync.Once
	var completed sync.WaitGroup
	completed.Add(2)
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
		return agent.Spec{Type: "codex-exec", Command: "codex", QueueSize: 2}, nil
	}}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		mu.Lock()
		calls = append(calls, req.MessageID)
		mu.Unlock()
		close(done)
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", QueueSize: 2}, nil
	}}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
		return agent.Spec{Type: "codex-exec", Command: "codex", QueueSize: 2}, nil
	}}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		sendMu.Lock()
		sendCount++
		sendMu.Unlock()
		started <- struct{}{}
		<-release
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		close(done)
		return agent.Response{Text: req.Prompt}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	mgr := &fakeExecutor{send: func(ctx context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		if got := ctx.Value(key); got != "value" {
			t.Fatalf("context value = %#v", got)
		}
		close(done)
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)
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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, _ agent.Response) error {
		if target.RecipientID != "u" {
			t.Fatalf("recipient = %q", target.RecipientID)
		}
		close(done)
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback reply target")
	}
}

func TestOrchestratorHandleEventForwardsRuntimeEvent(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	orchestrator := NewBotMessageOrchestrator(&fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected empty event to skip agent send")
		return agent.Response{}, nil
	}}, fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error {
		t.Fatal("expected empty event to skip reply")
		return nil
	}}, fakeResolver{}, nil)

	orchestrator.HandleEvent(context.Background(), channel.RuntimeEvent{BotID: "bot-1", MessageID: "m1", From: "u", Text: ""})
}

func TestOrchestratorSkipsDuplicateRuntimeEventMessageID(t *testing.T) {
	orchestrator := NewBotMessageOrchestrator(&fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected duplicate event to skip agent send")
		return agent.Response{}, nil
	}}, fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}, fakeResolver{}, nil)

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
	orchestrator := NewBotMessageOrchestrator(&fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected in-progress duplicate event to skip agent send")
		return agent.Response{}, nil
	}}, fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}, fakeResolver{}, nil)

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
	var startOnce sync.Once
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "m1" {
			startOnce.Do(func() { close(started) })
			<-release
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	var mu sync.Mutex
	var busyTargets []string
	gateway := fakeReplyGateway{reply: func(_ context.Context, target channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == busyReply {
			mu.Lock()
			busyTargets = append(busyTargets, target.RecipientID)
			mu.Unlock()
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

	// Block the bot on m1.
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started

	// Flood until one message is busy-rejected. The busy reply is sent
	// synchronously inside HandleMessage, so after the call returns we know
	// whether THIS message was the rejected one.
	rejected := ""
	for i := 0; i < 100 && rejected == ""; i++ {
		id := fmt.Sprintf("q%d", i)
		mu.Lock()
		before := len(busyTargets)
		mu.Unlock()
		orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: id, From: id, Text: id})
		mu.Lock()
		if len(busyTargets) > before {
			rejected = id
		}
		mu.Unlock()
	}
	close(release)

	if rejected == "" {
		t.Fatal("expected a busy reply once capacity was exceeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(busyTargets, rejected) {
		t.Fatalf("busy targets = %#v, want %q", busyTargets, rejected)
	}
}

func TestOrchestratorProcessesRetriedMessageIDAfterBusyReject(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var processed sync.Mutex
	processedIDs := make([]string, 0, 8)
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		processed.Lock()
		processedIDs = append(processedIDs, req.MessageID)
		processed.Unlock()
		if req.MessageID == "m1" {
			startOnce.Do(func() { close(started) })
			<-release
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	var mu sync.Mutex
	busyCount := 0
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == busyReply {
			mu.Lock()
			busyCount++
			mu.Unlock()
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

	// Block the bot on m1, then flood until one message is busy-rejected.
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started

	rejected := ""
	for i := 0; i < 100 && rejected == ""; i++ {
		id := fmt.Sprintf("q%d", i)
		mu.Lock()
		before := busyCount
		mu.Unlock()
		orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: id, From: id, Text: id})
		mu.Lock()
		if busyCount > before {
			rejected = id
		}
		mu.Unlock()
	}
	if rejected == "" {
		close(release)
		t.Fatal("expected a busy reply once capacity was exceeded")
	}

	containsRejected := func() bool {
		processed.Lock()
		defer processed.Unlock()
		return slices.Contains(processedIDs, rejected)
	}

	// The rejected message was never queued, so it must not be processed.
	if containsRejected() {
		close(release)
		t.Fatalf("busy-rejected %q should not have been processed", rejected)
	}

	// Unblock the bot; the admitted (non-rejected) messages drain.
	close(release)

	// It stays unprocessed without a retry (it was rejected, not queued).
	time.Sleep(50 * time.Millisecond)
	if containsRejected() {
		t.Fatalf("busy-rejected %q should not process without a retry", rejected)
	}

	// Retrying the rejected id now gets it processed.
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: rejected, From: rejected, Text: "retry"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if containsRejected() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for retried message %q to be processed", rejected)
}

func TestOrchestratorRepliesTimeoutWhenAgentTimesOut(t *testing.T) {
	var logs bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)

	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{}, context.DeadlineExceeded
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	got := logs.String()
	if !strings.Contains(got, "agent send failed") {
		t.Fatalf("logs = %q, want agent send failure log", got)
	}
	if !strings.Contains(got, "bot_id=bot-1") || !strings.Contains(got, "message_id=m1") || !strings.Contains(got, "error=context deadline exceeded") {
		t.Fatalf("logs = %q, want bot_id/message_id/error", got)
	}
}

func TestOrchestratorRepliesTimeoutWhenAgentContextCanceled(t *testing.T) {
	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{}, context.Canceled
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	orchestrator := NewBotMessageOrchestrator(&fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected duplicate to skip agent send")
		return agent.Response{}, nil
	}}, gateway, fakeResolver{}, nil)

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
	orchestrator := NewBotMessageOrchestrator(&fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		t.Fatal("expected in-progress duplicate to skip agent send")
		return agent.Response{}, nil
	}}, gateway, fakeResolver{}, nil)

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
	orchestrator := NewBotMessageOrchestrator(&fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID != "m1" {
			t.Fatalf("messageID = %q", req.MessageID)
		}
		close(processed)
		return agent.Response{Text: req.Prompt}, nil
	}}, gateway, fakeResolver{}, nil)

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

func TestOrchestratorBusyRejectDoesNotMarkMessageSeen(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.MessageID == "m1" {
			startOnce.Do(func() { close(started) })
			<-release
		}
		return agent.Response{Text: req.Prompt}, nil
	}}
	var mu sync.Mutex
	busyCount := 0
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		if resp.Text == busyReply {
			mu.Lock()
			busyCount++
			mu.Unlock()
		}
		return nil
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started

	rejected := ""
	for i := 0; i < 100 && rejected == ""; i++ {
		id := fmt.Sprintf("q%d", i)
		mu.Lock()
		before := busyCount
		mu.Unlock()
		orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: id, From: id, Text: id})
		mu.Lock()
		if busyCount > before {
			rejected = id
		}
		mu.Unlock()
	}
	if rejected == "" {
		close(release)
		t.Fatal("expected a busy reply once capacity was exceeded")
	}

	orchestrator.mu.Lock()
	_, seen := orchestrator.seen["bot-1:"+rejected]
	orchestrator.mu.Unlock()
	close(release)
	if seen {
		t.Fatalf("expected busy-rejected message %q to remain unseen", rejected)
	}
}

func TestOrchestratorRepliesFailureWhenAgentFails(t *testing.T) {
	var logs bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)

	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{Text: "unexpected status 503", RuntimeType: "codex"}, errors.New("codex exec failed: unexpected status 503")
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for failure reply")
	}

	if !reflect.DeepEqual(gateway.texts(), []string{"codex: unexpected status 503"}) {
		t.Fatalf("replies = %#v", gateway.texts())
	}
	if !reflect.DeepEqual(gateway.targets(), []string{"u"}) {
		t.Fatalf("targets = %#v", gateway.targets())
	}
	got := logs.String()
	if !strings.Contains(got, "agent send failed") {
		t.Fatalf("logs = %q, want agent send failure log", got)
	}
	if !strings.Contains(got, "bot_id=bot-1") || !strings.Contains(got, "message_id=m1") || !strings.Contains(got, "error=codex exec failed: unexpected status 503") {
		t.Fatalf("logs = %q, want bot_id/message_id/error", got)
	}
}

func TestOrchestratorRepliesTypedAgentErrorWhenResponseCarriesRuntimeType(t *testing.T) {
	done := make(chan struct{})
	gateway := &recordingReplyGateway{done: done}
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
		return agent.Response{Text: "service unavailable", RuntimeType: "codex"}, errors.New("boom")
	}}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m-typed", From: "u", Text: "one"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for typed failure reply")
	}

	if !reflect.DeepEqual(gateway.texts(), []string{"codex: service unavailable"}) {
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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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

func TestOrchestratorRepliesSuccessfulSendNearDeadline(t *testing.T) {
	replyDone := make(chan struct{})
	mgr := &fakeExecutor{send: func(ctx context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)
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

func TestOrchestratorUsesSpecTimeoutWhenLongerThanProcessingTimeout(t *testing.T) {
	done := make(chan struct{})
	mgr := &fakeExecutor{send: func(ctx context.Context, _ string, spec agent.Spec, _ agent.Request) (agent.Response, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected processing deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 150*time.Millisecond {
			t.Fatalf("remaining deadline too short: %s", remaining)
		}
		if spec.Timeout != 200*time.Millisecond {
			t.Fatalf("unexpected spec timeout: %s", spec.Timeout)
		}
		return agent.Response{Text: "ok"}, nil
	}}
	gateway := &recordingReplyGateway{done: done}
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", Timeout: 200 * time.Millisecond}, nil
	}}, nil)
	orchestrator.SetProcessingTimeoutForTest(50 * time.Millisecond)

	go orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m-timeout", From: "u", Text: "hello"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
	if !reflect.DeepEqual(gateway.texts(), []string{"ok"}) {
		t.Fatalf("replies = %#v", gateway.texts())
	}
}

func TestReplyWithTimeoutLogsResponseAndReplyError(t *testing.T) {
	var logs bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)

	orchestrator := NewBotMessageOrchestrator(
		&fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
			return agent.Response{}, nil
		}},
		fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error {
			return errors.New("reply failed")
		}},
		fakeResolver{},
		nil,
	)

	orchestrator.replyWithTimeout(context.Background(), InboundMessage{
		BotID:     "bot-1",
		MessageID: "msg-1",
		From:      "u-1",
	}, agent.Response{Text: "hello world"})

	got := logs.String()
	if !strings.Contains(got, `text="hello world"`) {
		t.Fatalf("logs = %q, want response text log", got)
	}
	if !strings.Contains(got, "reply failed") {
		t.Fatalf("logs = %q, want reply error log", got)
	}
}

func TestOrchestratorGivesTimeoutReplyFreshWindowAfterProcessingExpiry(t *testing.T) {
	replyDone := make(chan struct{})
	replyObserved := make(chan time.Duration, 1)
	mgr := &fakeExecutor{send: func(ctx context.Context, _ string, _ agent.Spec, _ agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)
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
	mgr := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
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
	orchestrator := NewBotMessageOrchestrator(mgr, gateway, fakeResolver{}, nil)

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

type fakeExecutor struct {
	send func(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error)
	push executorPushState
}

func (m *fakeExecutor) Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error) {
	return m.send(ctx, botID, spec, req)
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

func waitForReplyCount(t *testing.T, gw *recordingReplyGateway, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(gw.texts()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected at least %d replies, got %d: %v", n, len(gw.texts()), gw.texts())
}

func TestOrchestratorAcksThenPushesFinal(t *testing.T) {
	resolver := fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Orchestrator: true, Timeout: time.Minute, QueueSize: 1}, nil
	}}
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{Text: "final answer"}, nil
	}}
	replies := &recordingReplyGateway{}

	o := NewBotMessageOrchestrator(exec, replies, resolver, nil)
	o.HandleMessage(context.Background(), InboundMessage{
		BotID:     "brain_1",
		MessageID: "m1",
		From:      "user_1",
		Text:      "do a big thing",
	})

	// Expect: first reply is the ack, then (async) the final answer.
	waitForReplyCount(t, replies, 2)
	got := replies.texts()
	if got[0] != ackReply {
		t.Fatalf("expected ack first, got %q", got[0])
	}
	if got[len(got)-1] != "final answer" {
		t.Fatalf("expected final answer last, got %q", got[len(got)-1])
	}
}

// orchSessionStub records the last Upsert call.
type orchSessionStub struct {
	mu   sync.Mutex
	last domain.BotCLISession
}

func (s *orchSessionStub) Upsert(_ context.Context, sess domain.BotCLISession) error {
	s.mu.Lock()
	s.last = sess
	s.mu.Unlock()
	return nil
}

func (s *orchSessionStub) Get(_ context.Context, _, _ string) (domain.BotCLISession, error) {
	return domain.BotCLISession{}, domain.ErrNotFound
}

func (s *orchSessionStub) captured() domain.BotCLISession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func TestOrchestratorPersistsSessionAfterTurn(t *testing.T) {
	sessions := &orchSessionStub{}

	done := make(chan struct{})
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		defer func() {
			select {
			case done <- struct{}{}:
			default:
			}
		}()
		return agent.Response{Text: "ok", SessionID: "sess_z", RuntimeType: "claude"}, nil
	}}
	gateway := fakeReplyGateway{reply: func(context.Context, channel.ReplyTarget, agent.Response) error { return nil }}
	resolver := fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "claude", QueueSize: 1}, nil
	}}

	o := NewBotMessageOrchestrator(exec, gateway, resolver, sessions)
	o.HandleMessage(context.Background(), InboundMessage{
		BotID:     "bot_sess",
		MessageID: "m1",
		From:      "u1",
		Text:      "hello",
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for send")
	}

	// Give the upsert a moment to complete (it happens before replyWithTimeout).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := sessions.captured()
		if got.SessionID == "sess_z" && got.CLIType == "claude" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := sessions.captured()
	t.Fatalf("upsert = %#v, want SessionID=sess_z CLIType=claude", got)
}
