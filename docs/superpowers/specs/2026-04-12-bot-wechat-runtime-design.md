# Bot WeChat Runtime Design

## Goal
After a bot completes WeChat login, start a long-running WeChat runtime/loop for that bot, receive inbound WeChat messages, and print them to logs. Do not persist messages and do not send replies.

## Scope
This design adds a general bot connection manager and uses WeChat as the first runtime-backed channel implementation.

In scope:
- start a bot runtime after successful login
- restore bot runtimes on service startup
- receive inbound WeChat messages
- log inbound messages in structured form
- surface bot runtime state with existing bot status fields

Out of scope:
- message persistence
- auto reply
- model forwarding
- frontend message history
- automatic reconnect policy
- delivery acknowledgement persistence

## Recommended Approach
Introduce a process-local `BotConnectionManager` that owns the lifecycle of all active bot runtimes. Extend the channel provider contract with runtime startup capability so channel implementations can run long-lived receive loops.

For WeChat, implement a runtime that boots from stored credentials, establishes the provider-specific loop/connection, and emits inbound message events through a callback. The manager updates bot connection state and logs message events, but does not store them.

## Architecture

### 1. BotConnectionManager
Create `internal/app/bot_connection_manager.go`.

Responsibilities:
- start a runtime for one bot
- stop a runtime for one bot
- reject or replace duplicate starts for the same bot
- keep an in-memory map of active runtime handles by `bot_id`
- update bot status to `connecting`, `connected`, or `error`
- receive inbound runtime events and log them

Suggested internal state:
- `map[string]RuntimeHandle`
- mutex for concurrent start/stop access

`RuntimeHandle` should minimally contain:
- cancel function
- done channel or equivalent completion signal
- channel type

### 2. Provider runtime contract
Extend `internal/channel/provider.go` with runtime capabilities used after login.

Add a runtime starter interface alongside existing login/config methods. The design should allow other channels to implement the same contract later.

Suggested additions:
- `StartRuntime(ctx, req StartRuntimeRequest) (RuntimeHandle, error)`
- `RuntimeEventHandler func(RuntimeEvent)`

Suggested `StartRuntimeRequest` fields:
- `BotID`
- `ChannelType`
- `AccountUID`
- `CredentialPayload`
- `CredentialVersion`
- `OnEvent`
- `OnStateChange` or equivalent callback for connected/error transitions

Suggested `RuntimeEvent` fields:
- `BotID`
- `ChannelType`
- `MessageID`
- `From`
- `Text`
- `Raw`

The provider remains responsible for protocol details. The manager remains responsible for bot lifecycle and status changes.

### 3. WeChat runtime implementation
Create `internal/channel/wechat/runtime.go`.

Responsibilities:
- parse stored credential payload into the fields needed for runtime startup
- establish the WeChat-specific long connection or loop
- translate inbound provider messages into `RuntimeEvent`
- report `connected` once the runtime is ready
- report terminal failures through the state/error callback

The runtime should not:
- write to the database
- call the model
- send message replies

### 4. Login-success startup path
Update `internal/app/bot_service.go`.

When `RefreshLogin` confirms the binding:
1. upsert the channel account
2. update the bot account linkage
3. set bot state to `connecting`
4. invoke `BotConnectionManager.Start(botID)`
5. let manager drive the transition to `connected` or `error`

This keeps runtime boot orchestration out of the HTTP layer.

### 5. Startup restore path
Update `internal/bootstrap/bootstrap.go`.

After dependencies are assembled:
1. build `BotConnectionManager`
2. find bots that already have linked channel accounts
3. attempt runtime startup for those bots asynchronously
4. mark each bot `connecting` before startup
5. transition to `connected` or `error` based on runtime result

Startup restore failures must not block HTTP server startup.

## Data Flow
1. User scans QR and confirms login.
2. `RefreshLogin` stores credentials and links the bot to the channel account.
3. `RefreshLogin` requests runtime startup through `BotConnectionManager`.
4. Manager loads the bot/account context and starts the provider runtime.
5. WeChat runtime establishes its long-running loop.
6. Runtime signals readiness.
7. Manager marks the bot `connected`.
8. Runtime emits inbound message events.
9. Manager logs each event.
10. Runtime exits or fails.
11. Manager marks the bot `error` and removes the in-memory handle.

## Logging
Only structured logging is required.

Recommended log events:

| Event | Fields |
|---|---|
| runtime_start | `bot_id`, `channel_type` |
| runtime_connected | `bot_id`, `channel_type` |
| inbound_message | `bot_id`, `channel_type`, `message_id`, `from`, `text` |
| runtime_error | `bot_id`, `channel_type`, `error` |
| runtime_stopped | `bot_id`, `channel_type`, `reason` |

`Raw` payload may be logged only when useful for debugging and should remain bounded to avoid noisy logs.

## Error Handling

| Scenario | Expected behavior |
|---|---|
| credential payload cannot be parsed | runtime start fails, bot becomes `error` |
| provider runtime cannot start | bot becomes `error` |
| same bot started twice | manager rejects duplicate or restarts explicitly; default recommendation: reject duplicate |
| runtime loop exits unexpectedly | manager marks bot `error` and removes active handle |
| bootstrap restore start fails | log failure, mark bot `error`, continue app startup |

No automatic reconnect should be added in this phase.

## Testing Strategy
Add tests for:

### Manager tests
- start runtime for a logged-in bot
- reject duplicate start for same bot
- transition `connecting -> connected`
- transition to `error` on startup failure
- remove handle on stop/error
- log inbound message callback without persistence side effects

### Service tests
- `RefreshLogin` triggers runtime startup after successful login
- failed runtime startup leaves bot in `error`

### Bootstrap tests
- startup restore attempts runtimes for bots with linked accounts
- startup restore does not fail app bootstrap when one runtime fails

### WeChat runtime tests
- credential payload parsing
- inbound provider message becomes `RuntimeEvent`
- ready/error callback propagation

## Files to Change

| File | Purpose |
|---|---|
| `internal/app/bot_connection_manager.go` | new runtime lifecycle manager |
| `internal/app/bot_service.go` | trigger runtime startup after login |
| `internal/bootstrap/bootstrap.go` | construct manager and restore runtimes on startup |
| `internal/channel/provider.go` | runtime startup contract |
| `internal/channel/wechat/runtime.go` | WeChat runtime loop implementation |
| `internal/channel/wechat/provider.go` | implement runtime startup hook |
| `internal/channel/wechat/fake_provider.go` | deterministic runtime behavior for tests |
| `internal/app/*_test.go` | manager/service tests |
| `internal/bootstrap/bootstrap_test.go` | restore behavior tests |
| `internal/channel/wechat/*_test.go` | runtime tests |

## Decision Summary
Use a general `BotConnectionManager` now, but keep the first implementation narrow:
- WeChat only
- long-running receive loop only
- inbound logs only
- no persistence
- no replies
- no reconnect policy
