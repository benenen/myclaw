# Decoupled Request/Response Delivery — Design Spec

Date: 2026-07-16
Status: Approved (pending implementation plan)

## Problem

Today a bot turn is a **synchronous RPC**. The per-bot worker goroutine
(`runWorker` → `processMessage`) blocks inside `executor.Send` → `Session.Send`
→ `ACPRuntime.Run` for the entire turn (up to `spec.Timeout`), because `Run`
loops on `turnCh` until the CLI emits its `result` event. The final answer can
only come back as that call's return value, and it is the *same* worker
goroutine that then replies to the channel.

Consequences:

- While one turn runs, the per-bot queue (size 1 by default) fills and further
  messages are rejected with the "busy" reply — even though the agent could
  accept and queue them.
- The response path is welded to the request path: there is no lifetime-scoped
  "listen to the agent and deliver its results to the channel" goroutine; the
  `readLoop` that already pumps stdout for the agent's whole life only hands the
  `result` back to the *currently blocked* `Run` via `activeTurnCh`.

We want to split request (send into the agent) and response (deliver the
agent's result to the channel) into two independent goroutines, so the response
path is a **long-lived per-bot delivery loop** rather than the return value of a
blocking call.

## Goal

Per bot, run **two goroutines**:

- **accept (req)** — drains admitted messages, resolves the spec, acks (for
  orchestrator bots), and hands each message to the delivery loop. Never blocks
  on `Send`.
- **deliver (resp)** — a long-lived loop that serially runs each turn via the
  existing `executor.Send` and pushes each result **directly to the channel**.
  Lives for the bot's operational life (survives Claude process respawns),
  stopping only when the bot is disconnected/stopped.

Messages are accepted while a turn is in flight (pipelined), and results are
delivered FIFO — exactly one reply per incoming message.

Non-goals (v1): unsolicited / proactive agent messages (results with no
triggering user message); tagged multiplexing of concurrent conversations;
mid-turn writes / steering; any change to the `agent` layer or the driver
interface.

## Decisions (locked during brainstorming)

1. **Correlation** — FIFO, exactly one reply per incoming message. The agent
   never emits unsolicited output. Because `Send` is serial and blocking, the
   result of the in-flight turn always belongs to the current item — correlation
   is **structural**, not a fragile after-the-fact match over an async stream.
2. **Concurrency** — pipeline / fire immediately. The accept side forwards each
   message without waiting for the prior result; Claude processes turns serially
   internally, so end-to-end latency is unchanged, but the worker never blocks
   and messages are no longer rejected as "busy" while a turn runs.
3. **Approach A (reuse `Run`)** — the delivery loop wraps the existing blocking
   `executor.Send` (→ `Run` → `readLoop`). Chosen over Approach B (driver-level
   async: `Post` + `Results()` stream, dropping `Run`'s `turnCh` loop) and
   Approach C (all drivers async). B/C reimplement per-turn timeout, broken-state
   handling, and result correlation that `Run` already provides, for a benefit
   (true mid-turn writes) that is a non-goal. **No change to `agent.Session`,
   `agent.Manager`, `SessionRuntime`, or any driver.**
4. **Lifecycle 2A (bind to bot operational life)** — the deliver goroutine is
   per bot, spawned lazily on the first message, and **survives Claude process
   respawns** (a broken session is transparently replaced by
   `Manager.sessionFor` on the next `Send`). It stops only on bot
   disconnect/stop, via a new `orchestrator.StopBot(botID)` wired from the bot
   stop path. Chosen over 2B (moving the inbox + loop into `agent.Session` so its
   lifetime equals the exact process object) which churns the goroutine on every
   break, forces a "fail vs migrate queued items" decision, and touches the
   agent core. Investigation confirmed sessions are lazy and are only closed on
   spec change / broken inside `sessionFor`; there is no existing
   bot-stop → process-stop signal, so binding to the process object is awkward.
5. **Broken-session recovery** — inherited from today: a turn timeout/break marks
   the runtime broken; the *next* `Send` respawns a fresh Claude process (with
   `--resume` when a stored session id exists) and the queued messages run on it.
   Queued messages are **not** force-failed.
6. **Path unification** — the plain-bot and orchestrator-bot paths merge.
   `runOrchestratorTurn` is deleted. `spec.Orchestrator` now decides one thing
   only: whether to send an immediate ack at acceptance.

## Architecture & Data Flow

All changes live in `internal/app/bot`. `executor.Send`, the `agent` layer, and
the drivers are untouched.

```
channel webhook
 └► HandleMessage → admitMessage (dedup + busy) ──► acceptQueue
      │
      │  [accept goroutine · req]  (per bot; fast; never blocks on Send)
      │    spec  := resolver.Resolve(botID)
      │    sess  := beginProgress(WithoutCancel(ctx), target)   // construct only, no network
      │    if spec.Orchestrator { sess.Ack()  ||  text ack }    // immediate "收到"
      │    markMessageStarted(msg)                              // active++
      │    deliverInbox <- item{msg, spec, sess}
      │
      │  [deliver goroutine · resp]  (per bot; long-lived until StopBot; serial)
      │    for item := range deliverInbox:
      │      req.OnProgress = func(ev){ item.sess.Step(ctx, ev) }
      │      resp, err := executor.Send(detachedCtx+Timeout, botID, item.spec, req)
      │      if err  { item.sess.Fail(); reply(target, errText); finishMessage(false) }
      │      else    { persistSessionID(); item.sess.Done(); reply(target, resp); finishMessage(true) }
      │
      ▼  (reused, unchanged)
   agent.Manager → Session → ACPRuntime.Run → readLoop
   (Claude process + stdout pump; process-lifetime; respawned by sessionFor on break)
```

### Components

- **`acceptQueue chan InboundMessage`** — bounded admission buffer. `admitMessage`
  pushes non-blocking; full → "busy" reply. Preserves existing dedup (`seen`).
- **`deliverInbox chan deliveryItem`** — pipeline buffer, depth = `spec.QueueSize`.
  `deliveryItem = { msg InboundMessage; spec agent.Spec; sess channel.ProgressSession }`.
  When full, the accept goroutine blocks pushing → back-pressure propagates to
  `acceptQueue` → "busy" at admission.
- **accept goroutine** — replaces the body of today's `runWorker`/`processMessage`:
  resolve + ack + `markMessageStarted` + enqueue. Returns fast.
- **deliver goroutine** — long-lived; owns the serial `Send` + delivery
  (`reply`, session-id persist, `sess.Done/Fail`, `finishMessage`). Uses a
  detached context (`context.WithoutCancel` + `spec.Timeout`) so delivery is not
  tied to the inbound request context.

### Lifecycle

- Both goroutines spawn lazily on the first admitted message for a bot.
- **Removed:** idle reclaim (`idleTimer`, `reclaimWorker`, `workerIdleTimeout`).
- **Added:** `orchestrator.StopBot(botID)` — cancels the per-bot context, closes
  the channels, drains in-flight (in-flight `Send` is cancelled; its item is
  finished as failed without a channel reply since the bot is going away), and
  clears the bot's dedup/state. Wired from the bot stop path via a new `onStop`
  callback on `BotConnectionManager` (mirroring `onEvent`), invoked in
  `handleState` on `RuntimeStateStopped` / `RuntimeStateError` and in the
  explicit BotService stop path.
- The underlying Claude process is still owned by `agent.Manager` and is
  transparently respawned across breaks; the deliver goroutine is oblivious.

### Error / timeout

- Per-turn deadline = `spec.Timeout`, applied by the deliver goroutine around
  each `Send`. Only the in-flight (head) item has an active deadline; queued
  items are not yet timing.
- `Send` error → `timeoutReply` for `DeadlineExceeded`/`Canceled`, else
  `failedReply` (or `runtime + ": " + text` when the runtime returned text);
  `sess.Fail`; `finishMessage(false)`.
- A broken runtime is respawned by the next `Send` (see Decision 5).

## Behavior changes (user-visible)

- Plain bots now **accept messages while busy** (pipelined) instead of rejecting
  with "busy"; replies still arrive in order, one per message.
- Orchestrator bots behave as before (immediate ack, answer later) — the ack now
  simply happens on the accept goroutine.
- No change to reply content, dedup window, session-id resume, or the Feishu
  trace card.

## Testing (TDD, run with `-race`)

Reuse existing fakes (`fakeExecutor`, `fakeResolver`, `fakeReplyGateway`,
`fakeProgressReporter`/`fakeProgressSession`).

1. **FIFO delivery order** — m1/m2/m3 delivered to their respective targets in
   order.
2. **Accept while busy (pipeline)** — with a blocking `Send`, a second message is
   admitted (not "busy") and delivered after the first.
3. **Accept does not block on Send** — the accept goroutine keeps admitting while
   `Send` is blocked.
4. **Orchestrator ack at acceptance** — `Ack` happens before `Send` completes;
   the answer is delivered afterwards on the saved target.
5. **Error / timeout mapping** — `Send` error → `Fail` + `failedReply`; ctx
   deadline → `timeoutReply`.
6. **Broken then recover** — first `Send` errors, next succeeds → the queued
   message is still delivered.
7. **StopBot graceful shutdown** — after `StopBot`: no further deliveries,
   in-flight handled, `ActiveCount → 0`, no goroutine leak.
8. **Dedup** — duplicate `messageID` within TTL rejected.
9. **Progress wiring** — `Step`/`Done`/`Fail` invoked correctly (nil reporter →
   behaves as today).

No DB migration (schema version untouched). No UI change (`embed_test.go`
unaffected).

## Risks

- **Never-idle goroutines** — every bot that received a message keeps two
  goroutines until `StopBot`. Acceptable: a connected bot legitimately keeps its
  agent listener alive; goroutines are a few KB each and are torn down on
  disconnect.
- **StopBot wiring coverage** — if a bot stop path exists that does not reach the
  new `onStop` callback, its goroutines would leak until process exit. The plan
  must enumerate every bot-stop entry point and wire them.
- **Late `done` after reclaim of dedup state** — mitigated because `finishMessage`
  runs before `active` reaches 0 and re-looks-up state defensively.
```
