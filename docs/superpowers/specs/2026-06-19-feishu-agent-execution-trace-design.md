# Feishu Agent Execution Trace — Design Spec

Date: 2026-06-19
Status: Approved (pending implementation plan)

## Problem

Today a Feishu channel bot shows the user **only the final answer**. The agent
runs as one synchronous turn (`executor.Send` → driver `Run`), and the ACP
drivers parse but **discard** all intermediate execution — tool calls, thinking,
streamed chunks. The only progress feedback is canned text: orchestrator (brain)
bots send an immediate ack ("收到，正在处理…"); plain bots send nothing until the
answer; errors map to busy/timeout/failed strings.

Result: on a long turn (e.g. a 26s `boo ls` that builds a markdown table) the
user stares at silence and cannot tell whether the bot is working, wedged, or
dead. We want to surface **what the agent is doing** while it works.

## Goal

Show a **real-time tool-call trace** on Feishu while the agent runs, as a single
**interactive card updated in place** (`Message.Patch`), followed by the answer
as a **separate message** on the existing send path.

Non-goals (v1): streaming the answer body token-by-token; per-bot toggle UI;
showing tool *results/output*; non-ACP drivers.

## Decisions (locked during brainstorming)

1. **Content** — real-time tool-call trace (each step the agent runs), not body streaming.
2. **Message shape** — one trace card patched in place + a separate answer message (answer reuses the existing `SendText` path untouched, so rich markdown still renders as a card).
3. **Driver scope** — all three ACP drivers: claude, codex, opencode. Any driver without extraction degrades to "ack + answer, no trace lines".
4. **Trace granularity** — tool name + target, target truncated to ≤60 chars.
5. **Enablement** — global, default ON; env `CHANNEL_FEISHU_TRACE=0/false` disables. No schema change.
6. **Progress transport** — Approach A: an `OnProgress` callback field on `agent.Request` (see Architecture). Chosen over a channel param (B, invasive cross-layer signature changes) and an event bus (C, over-engineered).

## Architecture & Data Flow

```
Feishu WS message
 └► runtime.onMessage → RuntimeEvent → orchestrator (processMessage / runOrchestratorTurn)
      │
      │ 1. sess := progress.Begin(ctx, target)        // non-feishu / disabled / no creds → nil
      │ 2. if sess != nil { req.OnProgress = sess.Step } // nil → driver does zero extra work
      │
      ├─► executor.Send(ctx, spec, req) ── driver.Run
      │        readLoop parses a tool event
      │        └► req.OnProgress(ev) ─► sess.Step(ev) ─► [coalesce] PatchCard
      │                                       (card lazily Created on first tool event)
      │   Send returns (resp, err)
      ├─► err → sess.Fail(reason)   |   ok → sess.Done()   // final Patch: "✅ 完成 · N 步 · 26s" / "⚠️ 失败"
      │
      └─► replies.Reply(target, resp)   // answer: a separate 2nd message via existing SendText
```

**Key invariant** — the trace is a pure side-channel, best-effort. When `Begin`
returns nil (wechat/http target, trace disabled, or missing Feishu creds),
`OnProgress` is nil and the driver readLoop behaves byte-for-byte as today. Any
trace card failure only logs; it **never** affects whether/how the answer is sent.

**Lazy creation** — the trace card is `Create`d on the first tool event. A
zero-tool turn ("在吗" → "在") produces **no** trace card, only the answer.

**Why this injection point is clean** — verified that `agent.Request` flows
`Manager.Send → Session.Send → runtime.Run(ctx, req)` **untouched**. Adding a
field to `Request` reaches the driver with **zero** signature changes to the
executor/manager/session layers, and is nil-safe by construction.

## Components & Interfaces

### A. `internal/agent/types.go` — driver-level event (additive, nil-safe)

```go
type ProgressEvent struct {
    Kind   string // v1 fixed "tool"; reserved for "thinking" etc.
    Tool   string // canonical tool name: Bash/Read/Edit/Write/WebFetch/Grep/Glob/Task/...
    Target string // truncated target: command / file path / url (≤60 chars)
}

type Request struct {
    // ...existing fields unchanged...
    OnProgress func(ProgressEvent) // optional; nil = no tracing (current behavior)
}
```

### B. `internal/channel/progress.go` — channel-level abstraction (new file)

```go
type ProgressSession interface {
    Ack(ctx context.Context)                          // eager "🤖 处理中…" card; replaces orchestrator text ack
    Step(ctx context.Context, ev agent.ProgressEvent) // called from driver goroutine; concurrency-safe + throttled
    Done(ctx context.Context)                          // terminal: ✅ 完成 · N 步 · elapsed
    Fail(ctx context.Context, reason string)           // terminal: ⚠️ 失败/超时
}

type ProgressReporter interface {
    Begin(ctx context.Context, target ReplyTarget) ProgressSession // not applicable → nil
}

// MultiProgressReporter routes by target.ChannelType, mirroring MultiReplyGateway.
```

`channel` already imports `agent` (e.g. `ReplyGateway.Reply` takes `agent.Response`),
so reusing `agent.ProgressEvent` here keeps a single struct.

### C. `internal/channel/feishu/progress.go` — Feishu implementation (new file)

- `feishuProgressReporter.Begin` returns a real session only when
  `target.ChannelType == feishu`, trace is enabled, and creds resolve via the
  existing `registry`; otherwise nil.
- `*traceSession` holds: api, creds, target, `[]step`, card `messageID`,
  `lastPatch` time, a mutex, and a single flusher goroutine.
- Reuses the group thread/@ rules: the **trace card threads under the original
  message but does NOT @-mention** (the @ stays on the answer message).

### D. `internal/channel/feishu/api.go` — two new API methods

```go
CreateCard(ctx, creds, p CardParams) (messageID string, err error) // Message.Create, MsgTypeInteractive
PatchCard(ctx, creds, messageID, cardJSON string) error            // Message.Patch (SDK-verified)
```

Card JSON rendered by a new `buildProgressCard(state)`. `bootstrap.go` registers
a `multiProgressReporter` alongside the existing `multiReplyGateway` and injects
it into the orchestrator.

## Orchestrator Wiring (`internal/app/bot/message_orchestrator.go`)

New injected dependency `progress channel.ProgressReporter`, symmetric to `replies`.

| Path | Wiring |
|---|---|
| `processMessage` (plain bot, synchronous) | `sess := progress.Begin(target)`; `if sess!=nil { req.OnProgress = sess.Step }`; after Send `ok→sess.Done()` / `err→sess.Fail(reason)`; answer still via `replyWithTimeout(resp)`. **No Ack → card stays lazy**; zero-tool turn has no card. |
| `runOrchestratorTurn` (brain, detached) | `sess := Begin(target)`; `if sess!=nil { sess.Ack() } else { send text ackReply }` (**exactly one ack**); rest as above, inside the detached goroutine. |

## Per-Driver Tool Extraction (additive, only when `OnProgress != nil`)

Each driver stores an `activeProgress` mirroring its existing `activeTurnCh`
(set under lock in `Run`, cleared at turn end); readLoop invokes it on tool
events. Drivers emit the **canonical tool name + target**; emoji/layout are a
Feishu-side presentation concern (data/presentation separation).

| Driver | Current state | Change |
|---|---|---|
| **claude** (`driver_acp.go`) | handles only `system/init`, `result` | extend `claudeStreamEvent` to decode `type=="assistant"` → `message.content[]`; for blocks `type=="tool_use"` take `name` + `input` → `ProgressEvent` |
| **codex** (`driver_acp.go`) | has `handleItemStarted` for `item/started` | in `item/started`, recognize command/tool items, take command/target → emit |
| **opencode** (`driver_acp.go`) | `handleSessionUpdate` decodes only `contentItem(s)` text/delta | add decoding of the ACP `session/update` **tool_call variant** (`toolCallId/title/kind/rawInput`) → emit. **Highest uncertainty** — the implementation plan MUST first capture a real opencode `session/update` sample to confirm exact field names |

`extractTarget(name, input)`: Bash→command, Read/Edit/Write→file_path,
WebFetch/WebSearch→url/query, Grep/Glob→pattern, Task→description, else first
string field; truncated to ≤60 chars.

## Feishu Rendering & Throttle

**Tool→emoji map** (Feishu side): `🔧 Bash · 📖 Read · ✏️ Edit/Write · 🔍 Grep/Glob · 🌐 WebFetch · 🤖 Task · ▸ default`.

**`buildProgressCard(state)`** reuses the existing card skeleton
`{"config":{"wide_screen_mode":true},"elements":[{"tag":"markdown","content":...}]}`:
- header line: in-progress `🤖 处理中…` / done `✅ 完成 · N 步 · 26s` / fail `⚠️ 失败：<reason>` (optional Feishu card `header` color blue→green→red is a v2 nice-to-have)
- body: one line per step `emoji  Tool  Target`
- **cap**: show last 25 steps; if more, a top line `…(+K 步)` keeps the card bounded.

**Throttle (driver goroutine MUST NOT block on the network)** — each
`traceSession` runs **one flusher goroutine** that owns all Create/Patch calls.
`Step` only appends state under lock and wakes the flusher. Coalescing: patch
immediately if `now - lastPatch >= 700ms`, else a single pending timer flushes
the latest state at the boundary. `Done/Fail` force a **final frame** (always
sent) then stop the flusher. This serializes and rate-limits Feishu calls well
under the card-update QPS limit.

## Error Handling & Fallback (trace never drags down the answer)

- `CreateCard` fails (first frame / Ack) → log warn, session goes "degraded"
  (empty messageID), subsequent Step/Done become no-ops → answer still sent.
  For the **orchestrator Ack** case, if Create fails the session internally
  falls back to sending the plain-text `ackReply` once (it holds api+creds+target;
  the orchestrator stays unaware).
- `PatchCard` fails → log, keep state, retry on next flush.
- `Done/Fail` final frame fails → log; **answer unaffected**.
- Flusher lifecycle is bounded: both orchestrator paths **always** call Done or
  Fail after Send returns; the flusher also watches `ctx.Done()` to avoid leaks.
- `Step` runs no synchronous network call, so a slow Feishu API never blocks the
  driver readLoop.

## Config / Enablement (`internal/config`)

New `FeishuTrace bool`, default `true`; env `CHANNEL_FEISHU_TRACE=0/false`
disables. bootstrap: when disabled, do not register the Feishu reporter (or its
`Begin` always returns nil). Per-bot override is out of scope for v1.

## Testing

- **agent**: with `OnProgress` nil, existing driver tests stay green (regression).
  Add table tests "sample stdout lines → expected `[]ProgressEvent`" for claude
  (`tool_use`) and codex (`item/started`), reusing each `driver_acp_test.go`
  fake stdio harness.
- **channel/feishu**: `buildProgressCard` (in-progress/done/fail, truncation 60,
  cap 25+K); emoji map; **throttle** (fake clock + fake api recording calls: N
  rapid Steps → bounded Patch count, **Done always sends the final frame**);
  **degraded** (CreateCard error → no panic, separate answer path unaffected,
  Ack-failure → text-ack fallback); `Begin` returns nil for non-feishu/disabled.
- **app/bot**: fake ProgressReporter recording Ack/Step/Done/Fail — plain bot
  lazy (no Ack), Done on success / Fail on error/timeout, answer still replied;
  orchestrator calls Ack (replaces text ack); **nil session → byte-for-byte
  current behavior**.
- `go test ./...` green.

## Files Touched (summary)

New:
- `internal/channel/progress.go` — `ProgressEvent` reuse, `ProgressSession`, `ProgressReporter`, `MultiProgressReporter`
- `internal/channel/feishu/progress.go` — `feishuProgressReporter`, `traceSession`, `buildProgressCard`, emoji map
- test files alongside the above

Modified:
- `internal/agent/types.go` — `ProgressEvent`, `Request.OnProgress`
- `internal/agent/claude/driver_acp.go`, `internal/agent/codex/driver_acp.go`, `internal/agent/opencode/driver_acp.go` — `activeProgress` + tool extraction
- `internal/channel/feishu/api.go` + `types.go` — `CreateCard`/`PatchCard`, `CardParams`
- `internal/app/bot/message_orchestrator.go` — `progress` dependency + wiring
- `internal/config` — `FeishuTrace`
- `internal/bootstrap/bootstrap.go` — register/inject `multiProgressReporter`
