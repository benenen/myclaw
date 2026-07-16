# Decoupled Request/Response Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split each bot's turn into two per-bot goroutines — a non-blocking accept (req) loop and a long-lived deliver (resp) loop that runs `executor.Send` serially and pushes results straight to the channel — so messages are accepted while a turn is in flight and results are delivered FIFO by a goroutine bound to the bot's operational life.

**Architecture:** All changes live in `internal/app/bot`. The per-bot worker keeps admitting/deduping messages; a new persistent per-bot deliver goroutine wraps the existing blocking `executor.Send` (→ `Session.Send` → `ACPRuntime.Run` → `readLoop`, all unchanged). Correlation is structural (serial `Send` = one in-flight turn = one current reply target). Idle reclaim is replaced by an explicit `StopBot(botID)` wired from the bot-stop path.

**Tech Stack:** Go 1.23, standard `net/http`, GORM/SQLite, `context`, goroutines/channels. No new dependencies.

## Global Constraints

- Go 1.23; standard formatting (`gofmt`, tabs).
- **No changes** to the `agent` package, `SessionRuntime`, any driver, or the `executor.Send(ctx, botID, spec, req) (agent.Response, error)` signature.
- Correlation is FIFO, exactly one reply per incoming message; the agent never emits unsolicited output.
- Delivery runs on a **detached** context (`context.WithoutCancel(msg.Ctx)` + timeout) so the inbound request context ending does not kill the turn.
- Broken sessions are respawned by the next `Send` via `Manager.sessionFor` (existing behavior); queued messages are **not** force-failed.
- No DB migration (schema version untouched). No UI change (`internal/api/http/web/embed_test.go` unaffected).
- Every test run uses `-race`. Keep responses aligned with the existing `Envelope`/reply patterns. Conventional commit prefixes (`feat:`, `refactor:`, `test:`).
- Pre-existing flaky/race tests NOT introduced here (do not "fix" by changing this feature): `TestOrchestratorDedupesConcurrentDuplicateMessageIDPerBot`, `TestBotConnectionManagerMarksBotErrorOnErrorState`, `TestBotConnectionManagerMarksBotLoginRequiredOnSessionExpired`.

---

## File Structure

- `internal/app/bot/message_orchestrator.go` — **modified**. Replace the blocking `runWorker`/`processMessage`/`runOrchestratorTurn` with: an accept loop (`runAccept`/`accept`), a persistent deliver loop (`runDeliver`/`executeAndDeliver`/`deliverContext`/`isStopping`), and `StopBot`. Remove idle-reclaim machinery.
- `internal/app/bot/message_orchestrator_test.go` — **modified**. Remove 3 idle-reclaim tests; retarget 3 busy tests to the new capacity; drop the `SetWorkerIdleTimeoutForTest` call from the first-message test.
- `internal/app/bot/agent_delivery_test.go` — **created**. New behavior tests: FIFO delivery order, accept-while-busy pipelining, StopBot graceful shutdown.
- `internal/app/bot/connection_manager.go` — **modified**. Add an `onStop func(botID string)` callback, invoked on `RuntimeStateStopped`/`RuntimeStateError`.
- `internal/app/bot/connection_manager_test.go` — **modified**. Add a test that `onStop` fires on stop/error.
- `internal/bootstrap/bootstrap.go` — **modified**. Pass `orchestrator.StopBot` as the connection manager's `onStop`.

---

## Task 1: Two-goroutine turn (accept + persistent deliver), unify paths, enable pipelining, add StopBot

**Files:**
- Modify: `internal/app/bot/message_orchestrator.go`
- Modify (tests): `internal/app/bot/message_orchestrator_test.go`

**Interfaces:**
- Consumes: `executor.Send(ctx, botID, spec, req) (agent.Response, error)`; `channel.ProgressSession` (`Ack/Step/Done/Fail`); `domain.BotCLISessionRepository.Upsert`.
- Produces (used by later tasks): `func (o *BotMessageOrchestrator) StopBot(botID string)`.

### Production changes

- [ ] **Step 1: Add the `deliveryItem` type and per-bot delivery fields.**

In `message_orchestrator.go`, replace the `botState` struct and add `deliveryItem`:

```go
type deliveryItem struct {
	msg  InboundMessage
	spec agent.Spec
	sess channel.ProgressSession
}

type botState struct {
	worker    *botWorker
	pending   *InboundMessage
	handoff   chan deliveryItem // accept -> deliver, unbuffered
	stopCh    chan struct{}     // closed by StopBot to stop both goroutines
	active    int
	queueSize int
}
```

Remove the `workerIdleTime` field from `BotMessageOrchestrator` and the `workerIdleTimeout`/`cleanupInterval`-adjacent idle constant usage. In the const block, delete `workerIdleTimeout` and `finishWaitPollInterval` (only used by removed code). Keep `defaultQueueSize`, `processingTimeout`, `replyTimeout`, `seenMessageTTL`, `cleanupInterval`, and all reply strings.

In `NewBotMessageOrchestrator`, delete the `workerIdleTime: workerIdleTimeout,` line.

- [ ] **Step 2: Extend `ensureWorker` to spawn both goroutines.**

Replace `ensureWorker` with:

```go
func (o *BotMessageOrchestrator) ensureWorker(botID string, state *botState) {
	o.mu.Lock()
	if state.worker != nil {
		o.mu.Unlock()
		return
	}
	pending := state.pending
	state.pending = nil
	worker := &botWorker{queue: make(chan InboundMessage, defaultQueueSize)}
	state.worker = worker
	state.handoff = make(chan deliveryItem)
	state.stopCh = make(chan struct{})
	o.mu.Unlock()

	go o.runAccept(botID, worker, state)
	go o.runDeliver(botID, state)
	if pending != nil {
		worker.queue <- *pending
	}
}
```

- [ ] **Step 3: Replace `runWorker` with `runAccept` (no idle timer).**

Delete `runWorker` entirely and add:

```go
// runAccept drains admitted messages, resolves + acks each, and hands it to the
// deliver goroutine. It never blocks on Send. It exits when StopBot closes stopCh.
func (o *BotMessageOrchestrator) runAccept(botID string, worker *botWorker, state *botState) {
	for {
		select {
		case <-state.stopCh:
			return
		case msg := <-worker.queue:
			item, ok := o.accept(botID, msg)
			if !ok {
				continue
			}
			select {
			case state.handoff <- item:
			case <-state.stopCh:
				o.finishMessage(item.msg, false)
				return
			}
		}
	}
}
```

- [ ] **Step 4: Add `accept` (front half of the old `processMessage`).**

```go
// accept resolves the spec, acks orchestrator bots, marks the message started,
// and returns the item to hand to the deliver goroutine. On resolver failure it
// replies + finishes and returns ok=false.
func (o *BotMessageOrchestrator) accept(botID string, msg InboundMessage) (deliveryItem, bool) {
	o.markMessageStarted(msg)
	resolveCtx, cancelResolve := o.processingContext(msg.Ctx, 0)
	spec, err := o.resolver.Resolve(resolveCtx, botID)
	cancelResolve()
	if err != nil {
		log.Printf("resolver failed: bot_id=%s message_id=%s error=%v", msg.BotID, msg.MessageID, err)
		o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: failedReply})
		o.finishMessage(msg, false)
		return deliveryItem{}, false
	}
	queueSize := spec.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	o.resizeWorkerQueue(botID, queueSize)

	base := context.WithoutCancel(msg.Ctx)
	sess := o.beginProgress(base, msg.ReplyTarget)
	if spec.Orchestrator {
		if sess != nil {
			if err := sess.Ack(base); err != nil {
				sess = nil
				o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})
			}
		} else {
			o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})
		}
	}
	return deliveryItem{msg: msg, spec: spec, sess: sess}, true
}
```

> Note: `context.WithoutCancel(nil)` panics; `msg.Ctx` is always set by `attachContext` in `HandleMessage`, but `accept` is only reached through that path so `msg.Ctx` is non-nil. If a future caller bypasses it, guard with `base := context.Background(); if msg.Ctx != nil { base = context.WithoutCancel(msg.Ctx) }`.

- [ ] **Step 5: Delete `runOrchestratorTurn` and `processMessage`; add the deliver loop.**

Delete `runOrchestratorTurn` and `processMessage` entirely. Add:

```go
// runDeliver serially runs each turn and pushes its result to the channel.
// It is the long-lived resp goroutine; it exits only when StopBot closes stopCh.
func (o *BotMessageOrchestrator) runDeliver(botID string, state *botState) {
	for {
		select {
		case <-state.stopCh:
			return
		case item := <-state.handoff:
			o.executeAndDeliver(botID, state, item)
		}
	}
}

// executeAndDeliver runs one turn via the blocking executor.Send and delivers
// the outcome: reply + progress finalize + session persist + dedup finish.
func (o *BotMessageOrchestrator) executeAndDeliver(botID string, state *botState, item deliveryItem) {
	ctx, cancel := o.deliverContext(state, item.msg, item.spec.Timeout)
	defer cancel()

	req := agent.Request{
		BotID:     item.msg.BotID,
		UserID:    item.msg.From,
		MessageID: item.msg.MessageID,
		Prompt:    item.msg.Text,
	}
	if item.sess != nil {
		req.OnProgress = func(ev agent.ProgressEvent) { item.sess.Step(ctx, ev) }
	}

	resp, err := o.executor.Send(ctx, item.msg.BotID, item.spec, req)
	if err != nil {
		if o.isStopping(state) {
			o.finishMessage(item.msg, false)
			return
		}
		log.Printf("agent send failed: bot_id=%s message_id=%s error=%v", item.msg.BotID, item.msg.MessageID, err)
		replyText := failedReply
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			replyText = timeoutReply
		} else if resp.RuntimeType != "" && strings.TrimSpace(resp.Text) != "" {
			replyText = resp.RuntimeType + ": " + strings.TrimSpace(resp.Text)
		}
		if item.sess != nil {
			item.sess.Fail(ctx, replyText)
		}
		o.replyWithTimeout(ctx, item.msg, agent.Response{Text: replyText})
		o.finishMessage(item.msg, false)
		return
	}

	if o.sessions != nil && strings.TrimSpace(resp.SessionID) != "" && resp.SessionID != item.spec.ResumeSessionID {
		if err := o.sessions.Upsert(context.WithoutCancel(ctx), domain.BotCLISession{
			BotID:     item.msg.BotID,
			CLIType:   resp.RuntimeType,
			SessionID: resp.SessionID,
			WorkDir:   item.spec.WorkDir,
		}); err != nil {
			log.Printf("cli session upsert failed: bot_id=%s cli=%s error=%v", item.msg.BotID, resp.RuntimeType, err)
		}
	}
	if item.sess != nil {
		item.sess.Done(ctx)
	}
	o.replyWithTimeout(ctx, item.msg, resp)
	o.finishMessage(item.msg, true)
}

// deliverContext builds the detached per-turn context: request values preserved,
// inbound cancellation dropped, a per-turn timeout applied, and StopBot able to
// interrupt the in-flight Send.
func (o *BotMessageOrchestrator) deliverContext(state *botState, msg InboundMessage, specTimeout time.Duration) (context.Context, context.CancelFunc) {
	o.mu.Lock()
	timeout := o.processingTimeout
	o.mu.Unlock()
	if specTimeout > timeout {
		timeout = specTimeout
	}
	base := context.Background()
	if msg.Ctx != nil {
		base = context.WithoutCancel(msg.Ctx)
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(base, timeout)
	} else {
		ctx, cancel = context.WithCancel(base)
	}
	// Link StopBot: closing stopCh cancels the in-flight turn.
	go func() {
		select {
		case <-state.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (o *BotMessageOrchestrator) isStopping(state *botState) bool {
	select {
	case <-state.stopCh:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 6: Add `StopBot`; remove idle-reclaim helpers.**

Delete `reclaimWorker`, `waitForQueueCapacity`, `queueCapacity`, and `SetWorkerIdleTimeoutForTest`. Add:

```go
// StopBot stops the bot's accept/deliver goroutines and clears its state. Bound
// to the bot's operational life: called when the bot disconnects/stops. Safe to
// call for an unknown bot (no-op). The in-flight Send (if any) is cancelled.
func (o *BotMessageOrchestrator) StopBot(botID string) {
	o.mu.Lock()
	state, ok := o.bots[botID]
	if !ok {
		o.mu.Unlock()
		return
	}
	if state.stopCh != nil {
		select {
		case <-state.stopCh:
		default:
			close(state.stopCh)
		}
	}
	delete(o.bots, botID)
	for key := range o.seen {
		if strings.HasPrefix(key, botID+":") {
			delete(o.seen, key)
		}
	}
	o.mu.Unlock()
}
```

- [ ] **Step 7: Verify the file compiles (no idle references remain).**

Run: `cd /root/workspace/master/myclaw && go build ./internal/app/bot/`
Expected: PASS. If it fails with "undefined: workerIdleTimeout / reclaimWorker / finishWaitPollInterval / runWorker / processMessage / runOrchestratorTurn / SetWorkerIdleTimeoutForTest / waitForQueueCapacity / queueCapacity", delete the remaining reference — those symbols are all removed in this task.

### Test changes (retarget the 6 tests that encode old behavior)

- [ ] **Step 8: Remove the 3 idle-reclaim tests.**

Delete these entire functions from `message_orchestrator_test.go` (they assert idle reclaim, which no longer exists; StopBot shutdown is covered in Task 3):
- `TestOrchestratorReclaimsBotStateAfterQueueDrains`
- `TestOrchestratorReclaimsAfterStuckSendTimeout`
- `TestOrchestratorReclaimsAfterStuckReplyTimeout`

- [ ] **Step 9: Drop the idle-timeout setter from the first-message test.**

In `TestOrchestratorCreatesWorkerAndProcessesFirstMessage`, delete the line:

```go
	orchestrator.SetWorkerIdleTimeoutForTest(200 * time.Millisecond)
```

The rest of the test is unchanged and still valid: it admits `m1`, asserts `state.worker != nil`, and waits for `seen["bot-1:m1"].inProgress` to clear.

- [ ] **Step 10: Retarget `TestOrchestratorRepliesBusyWhenQueueFull` to the new capacity.**

With pipelining the accept capacity before a busy reject is 3 (1 in `Send`, 1 blocked at `handoff`, 1 in the queue), so the 4th concurrent message is the one rejected. Replace the body from the `HandleMessage` for `m2` onward:

```go
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u2", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m3", From: "u3", Text: "three"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m4", From: "u4", Text: "four"})
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
	if !slices.Contains(targets, "u4") {
		t.Fatalf("targets = %#v", targets)
	}
```

- [ ] **Step 11: Retarget `TestOrchestratorBusyRejectDoesNotMarkMessageSeen`.**

Same capacity fix. Send `m4`/`u4` as the rejected message and assert `m4` is unseen. Replace from the `m2` `HandleMessage` through the seen check:

```go
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m2", From: "u2", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m3", From: "u3", Text: "three"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m4", From: "u4", Text: "four"})
	select {
	case <-busyReplySent:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for busy reply")
	}

	orchestrator.mu.Lock()
	_, seen := orchestrator.seen["bot-1:m4"]
	orchestrator.mu.Unlock()
	if seen {
		t.Fatal("expected busy-rejected message to remain unseen")
	}
```

(The rest — `close(release)` then waiting for `secondStarted` on `m2` — is unchanged.)

- [ ] **Step 12: Retarget `TestOrchestratorProcessesRetriedMessageIDAfterBusyReject`.**

Add a filler message so `retry-me` is the 4th (rejected) message. Replace the three `HandleMessage` sends after `<-started` with:

```go
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "m1", From: "u1", Text: "one"})
	<-started
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "queued", From: "u2", Text: "two"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "filler", From: "u3", Text: "three"})
	orchestrator.HandleMessage(context.Background(), InboundMessage{BotID: "bot-1", MessageID: "retry-me", From: "u4", Text: "four"})
```

And update the final assertion to include `filler` in processing order:

```go
	if !reflect.DeepEqual(processedIDs, []string{"m1", "queued", "filler", "retry-me"}) {
		t.Fatalf("processedIDs = %#v", processedIDs)
	}
```

(The `fakeExecutor` blocks only `m1` on `release` and closes `secondProcessed` on `retry-me`; `queued` and `filler` return immediately once `m1` releases. The retry of `retry-me` after `close(release)` is unchanged.)

- [ ] **Step 13: Run the orchestrator package tests.**

Run: `cd /root/workspace/master/myclaw && go test ./internal/app/bot/ -race`
Expected: PASS (except the known pre-existing flakes listed in Global Constraints, which may intermittently fail on `-race`; re-run to confirm they are the only failures and are unrelated).

- [ ] **Step 14: Commit.**

```bash
cd /root/workspace/master/myclaw
git add internal/app/bot/message_orchestrator.go internal/app/bot/message_orchestrator_test.go
git commit -m "refactor(bot): split turn into accept + persistent deliver goroutines

Replace the blocking per-bot worker with an accept loop (resolve/ack/enqueue)
and a long-lived deliver loop that runs executor.Send serially and delivers
results to the channel. Unifies the plain and orchestrator paths, enables
pipelined acceptance, and replaces idle reclaim with StopBot.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Wire `StopBot` from the bot-stop path

**Files:**
- Modify: `internal/app/bot/connection_manager.go`
- Test: `internal/app/bot/connection_manager_test.go`
- Modify: `internal/bootstrap/bootstrap.go`

**Interfaces:**
- Consumes: `orchestrator.StopBot(botID string)` (Task 1).
- Produces: `NewBotConnectionManagerWithCallbacks(..., onEvent, onStop)` — a new trailing `onStop func(string)` parameter.

- [ ] **Step 1: Write the failing test for the `onStop` callback.**

Add to `connection_manager_test.go`:

```go
func TestBotConnectionManagerInvokesOnStopWhenStopped(t *testing.T) {
	var mu sync.Mutex
	var stopped []string
	onStop := func(botID string) {
		mu.Lock()
		stopped = append(stopped, botID)
		mu.Unlock()
	}
	bot := domain.Bot{ID: "bot-1", ChannelType: "wechat", ChannelAccountID: "acc-1"}
	m := NewBotConnectionManagerWithCallbacks(newBotRepoStub(bot), nil, nil, nil, nil, nil, onStop)

	m.handleState(bot, channel.RuntimeStateEvent{State: channel.RuntimeStateStopped})

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(stopped, []string{"bot-1"}) {
		t.Fatalf("stopped = %#v", stopped)
	}
}
```

Add `"reflect"` and `"sync"` to the test file imports if not already present.

- [ ] **Step 2: Run the test to verify it fails.**

Run: `cd /root/workspace/master/myclaw && go test ./internal/app/bot/ -run TestBotConnectionManagerInvokesOnStopWhenStopped`
Expected: FAIL — `NewBotConnectionManagerWithCallbacks` takes 6 args, not 7 (does not compile).

- [ ] **Step 3: Add the `onStop` field and parameter.**

In `connection_manager.go`, add the field to the struct:

```go
	onEvent  func(channel.RuntimeEvent)
	onStop   func(botID string)
```

Change the constructor chain. `NewBotConnectionManagerWithCallbacks` gets a trailing param and sets the field:

```go
func NewBotConnectionManagerWithCallbacks(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, cipher *security.Cipher, logger *logging.Logger, onEvent func(channel.RuntimeEvent), onStop func(botID string)) *BotConnectionManager {
	return &BotConnectionManager{
		handles:  make(map[string]channel.RuntimeHandle),
		bots:     bots,
		accounts: accounts,
		starter:  starter,
		cipher:   cipher,
		logger:   logger,
		onEvent:  onEvent,
		onStop:   onStop,
	}
}
```

Update the two thin constructors to pass `nil` for `onStop`:

```go
func NewBotConnectionManager(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, logger *logging.Logger) *BotConnectionManager {
	return NewBotConnectionManagerWithCallbacks(bots, accounts, starter, nil, logger, nil, nil)
}

func NewBotConnectionManagerWithCipher(bots domain.BotRepository, accounts domain.ChannelAccountRepository, starter channel.RuntimeStarter, cipher *security.Cipher, logger *logging.Logger) *BotConnectionManager {
	return NewBotConnectionManagerWithCallbacks(bots, accounts, starter, cipher, logger, nil, nil)
}
```

- [ ] **Step 4: Invoke `onStop` on stop/error.**

Add a helper and call it in `handleState`:

```go
func (m *BotConnectionManager) notifyStop(botID string) {
	if m.onStop != nil {
		m.onStop(botID)
	}
}
```

In `handleState`, in both the `RuntimeStateError` and `RuntimeStateStopped` cases, add `m.notifyStop(bot.ID)` next to the existing `m.remove(bot.ID)`:

```go
	case channel.RuntimeStateError:
		// ... existing bot.ConnectionStatus / Update ...
		m.remove(bot.ID)
		m.notifyStop(bot.ID)
	case channel.RuntimeStateStopped:
		m.remove(bot.ID)
		m.notifyStop(bot.ID)
```

- [ ] **Step 5: Run the new test.**

Run: `cd /root/workspace/master/myclaw && go test ./internal/app/bot/ -run TestBotConnectionManagerInvokesOnStopWhenStopped -race`
Expected: PASS.

- [ ] **Step 6: Wire it in bootstrap.**

In `internal/bootstrap/bootstrap.go`, the connection manager is created at line ~128. It is constructed before `botSvc`, and `orchestrator` already exists at line 119. Update the call to pass `orchestrator.StopBot`:

```go
	botManager := bot.NewBotConnectionManagerWithCallbacks(botRepo, accountRepo, multiProvider, cipher, logger, func(ev channel.RuntimeEvent) {
		orchestrator.HandleEvent(context.Background(), ev)
	}, func(botID string) {
		orchestrator.StopBot(botID)
	})
```

- [ ] **Step 7: Build the whole module and run the bot + bootstrap packages.**

Run: `cd /root/workspace/master/myclaw && go build ./... && go test ./internal/app/bot/ ./internal/bootstrap/ -race`
Expected: PASS (module builds; both test packages green, modulo the known pre-existing flakes).

- [ ] **Step 8: Commit.**

```bash
cd /root/workspace/master/myclaw
git add internal/app/bot/connection_manager.go internal/app/bot/connection_manager_test.go internal/bootstrap/bootstrap.go
git commit -m "feat(bot): stop orchestrator delivery goroutines on bot disconnect

Add an onStop callback to BotConnectionManager, fired on RuntimeStateStopped
and RuntimeStateError, wired in bootstrap to orchestrator.StopBot so the per-bot
accept/deliver goroutines are torn down when the bot stops.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: New behavior tests (FIFO order, pipeline, StopBot shutdown)

**Files:**
- Create: `internal/app/bot/agent_delivery_test.go`

**Interfaces:**
- Consumes: `NewBotMessageOrchestrator`, `HandleMessage`, `StopBot`, `HasBotState`, `ActiveCount`, and the existing fakes (`fakeExecutor`, `fakeReplyGateway`/`recordingReplyGateway`, `fakeResolver`) defined in `message_orchestrator_test.go` (same package `bot`).

- [ ] **Step 1: Write the FIFO-order test.**

Create `internal/app/bot/agent_delivery_test.go`:

```go
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
```

- [ ] **Step 2: Run it; expect PASS.**

Run: `cd /root/workspace/master/myclaw && go test ./internal/app/bot/ -run TestDeliverPreservesFIFOOrderPerBot -race`
Expected: PASS.

- [ ] **Step 3: Write the accept-while-busy (pipeline) test.**

Append to `agent_delivery_test.go`:

```go
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
```

- [ ] **Step 4: Run it; expect PASS.**

Run: `cd /root/workspace/master/myclaw && go test ./internal/app/bot/ -run TestAcceptWhileBusyPipelinesSecondMessage -race`
Expected: PASS.

- [ ] **Step 5: Write the StopBot graceful-shutdown test.**

Append to `agent_delivery_test.go`:

```go
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
```

- [ ] **Step 6: Run it; expect PASS.**

Run: `cd /root/workspace/master/myclaw && go test ./internal/app/bot/ -run TestStopBotCancelsInFlightAndRemovesState -race`
Expected: PASS.

- [ ] **Step 7: Full package + module verification with `-race`.**

Run: `cd /root/workspace/master/myclaw && go test ./... -race`
Expected: PASS. The only acceptable failures are the pre-existing flakes named in Global Constraints — if any of those fail, re-run `go test ./internal/app/bot/ -race -count=3` and confirm the new tests (`TestDeliver*`, `TestAcceptWhileBusy*`, `TestStopBot*`) and the retargeted busy tests are stable across runs.

- [ ] **Step 8: Commit.**

```bash
cd /root/workspace/master/myclaw
git add internal/app/bot/agent_delivery_test.go
git commit -m "test(bot): FIFO delivery, pipelined acceptance, StopBot shutdown

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Two goroutines (accept req / deliver resp) → Task 1 Steps 2–5.
- FIFO / structural correlation → Task 1 (serial `runDeliver`) + Task 3 Step 1.
- Pipeline / accept-while-busy → Task 1 (`runAccept` non-blocking) + Task 3 Step 3; busy retargeting Task 1 Steps 10–12.
- Detached delivery context → Task 1 `deliverContext`.
- Broken-session respawn unchanged → reuse of `executor.Send` (no agent-layer change); asserted indirectly by existing `TestOrchestratorPersistsSessionAfterTurn` and unchanged timeout tests.
- Path unification / delete `runOrchestratorTurn` → Task 1 Step 5; ack-at-acceptance preserved (existing `TestOrchestratorAcksThenPushesFinal` + progress tests stay green).
- Lifecycle 2A / StopBot on disconnect → Task 1 Step 6 + Task 2; graceful shutdown → Task 3 Step 5.
- No migration / no UI → no schema or web files touched.

**Placeholder scan:** none — every step has concrete code or an exact command + expected output.

**Type consistency:** `deliveryItem{msg, spec, sess}`, `botState{worker, pending, handoff, stopCh, active, queueSize}`, `StopBot(botID string)`, `runAccept`/`accept`/`runDeliver`/`executeAndDeliver`/`deliverContext`/`isStopping`, and `onStop func(botID string)` are used consistently across tasks. `finishMessage` (not the removed `finishMessageEventually`) is called directly in new code — note `finishMessageEventually` still exists and simply wraps `finishMessage`; either compiles, but new code uses `finishMessage` directly. Existing callers of `finishMessageEventually` are removed with `processMessage`/`runOrchestratorTurn`, so `finishMessageEventually` becomes unused — delete it in Task 1 Step 6 to avoid an unused-method lint (Go does not error on unused methods, so this is optional cleanup; if kept, it is harmless).
