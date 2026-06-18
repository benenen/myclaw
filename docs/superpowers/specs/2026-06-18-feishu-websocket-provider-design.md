# Feishu WebSocket Provider Design

## Goal
Add a Feishu (飞书/Lark) channel that connects via the Feishu **WebSocket long-connection** mode, receives inbound messages, forwards them through the existing bot orchestrator, and replies back into Feishu. A Feishu bot authenticates with a per-bot **App ID + App Secret** (self-built app), not a QR scan.

## Scope

In scope:
- New `internal/channel/feishu/` package implementing `channel.Provider`, `channel.RuntimeStarter`, and `channel.ReplyGateway`.
- Per-bot App ID / App Secret credential model, threaded from the connect step into the provider via a generic `Config map[string]string`.
- WebSocket long-connection runtime per bot (one Feishu app = one wss), using the official `oapi-sdk-go/v3` high-level `channel` module.
- Single-chat (p2p) and group-chat (@mention) message handling.
- Reply into Feishu: p2p direct send; group reply-to-original-message.
- Frontend: create-bot modal + connect card capture App ID / App Secret; add "飞书" as a channel option.
- Unit tests behind a fake SDK seam (no real wss in tests).

Out of scope:
- Message persistence / history (consistent with existing channels).
- Card / media / file messages (text + markdown only for v1).
- Global (shared) Feishu app across bots — explicitly rejected in favor of per-bot.
- Store-app (ISV) auth flow; only self-built app credentials.
- Custom reconnect policy (SDK handles reconnect internally).

## Decisions (locked during brainstorming)
1. **Credential source:** per-bot App ID / App Secret (not global env).
2. **Receive path:** prefer the SDK high-level `channel` module (`OnMessage`/`Send`); fall back to low-level `larkws` + `EventDispatcher` only if `NormalizedMessage` does not expose `chat_type` + `mentions`. Hidden behind an internal interface so the fallback does not touch provider/runtime.
3. **Threading:** add a generic `Config map[string]string` to the binding requests (open to all providers), not Feishu-specific typed fields.
4. **Frontend:** included in this change.

## Architecture

### 1. New package `internal/channel/feishu/`

Mirrors the structure of `internal/channel/wechat/` and `internal/channel/httpchan/`.

| File | Responsibility |
|---|---|
| `client.go` | Wrap the SDK. Build `lark.NewClient(appID, appSecret)` (API) + `larkws.NewClient(appID, appSecret)` (WS) + `channel.NewChannel(...)`. `ValidateApp(ctx, appID, secret)` → fetch bot info (`/open-apis/bot/v3/info`) to verify creds and return `{appName, botOpenID}`. Define the internal `feishuChannel` interface that `runtime.go`/`reply_gateway.go` depend on. |
| `registry.go` | Process-local `map[botID]*conn` holding the live `feishuChannel`. Shared between Provider/runtime (writer) and ReplyGateway (reader), guarded by a mutex. Mirrors `httpchan.Receiver`. `Register`/`Unregister`/`Lookup`. |
| `provider.go` | Implements `channel.Provider`. `CreateBinding` returns a binding ref immediately (no QR). `RefreshBinding` calls `ValidateApp`, returns `CredentialPayload = {app_id, app_secret, bot_open_id, app_name}`, `AccountUID = app_id`, `DisplayName = app_name`, `ProviderStatus = confirmed`. `BuildRuntimeConfig` passes the credential blob through. |
| `runtime.go` | Implements `channel.RuntimeStarter`. `StartRuntime` decrypts credential payload, builds the `feishuChannel`, registers `OnMessage`, stores it in the registry, runs `ch.Start` in a goroutine. Emits `OnState`: `Connected` on `OnReady`, `Stopped` on ctx cancel (`ch.Stop`), `Error` on fatal `ch.Start` return. `RuntimeHandle.Stop` cancels + `ch.Stop` + registry unregister. |
| `reply_gateway.go` | Implements `channel.ReplyGateway`. Looks up the `feishuChannel` by `bot_id` from the registry. p2p → `Send{ChatID, Text}`; group → `Send{ChatID, Text, ReplyMessageID, Mentions:[sender]}`. |
| `config.go` | Thin: log level, lark domain (Feishu vs Lark/global), self-built default. Credentials are per-bot, not here. |

### 2. Credential threading (cross-cutting, additive)

Reuses the existing **auto-confirm** path (the same mechanism `http` uses to skip the QR scan).

- `channel.CreateBindingRequest` and `channel.RefreshBindingRequest`: add `Config map[string]string`. Additive — existing wechat/http callers are unaffected.
- `dto.ConnectBotRequest`: add `app_id` and `app_secret` (json, `omitempty`).
- `handlers.ConnectBot`: map them into `StartBotLoginInput.Config`.
- `bot.StartBotLoginInput`: add `Config map[string]string`. `BotService.StartLogin` passes `Config` into `CreateBindingRequest`, and (for auto-confirm) forwards it into `confirmAndStartRuntime` → `RefreshBindingRequest`. Within one `StartLogin` call CreateBinding and RefreshBinding run back-to-back, so the config is passed in memory and never needs to be persisted on the binding row.
- `isAutoConfirmChannel`: add `"feishu"`.
- The returned `CredentialPayload` is encrypted into `ChannelAccount` via the existing cipher path; `StartRuntime` receives it via the existing `StartRuntimeRequest.CredentialPayload` and decrypts.

**Secret handling:** App Secret lives only in (a) the encrypted `ChannelAccount` row and (b) registry memory. It MUST NOT appear in `ReplyTarget.Metadata`, `RuntimeEvent`, logs, or agent context. Replies resolve the client via `bot_id` → registry, never via a secret in metadata.

### 3. Message flow & group @mention

- **p2p:** respond to every inbound message. `OnEvent` → orchestrator. Reply: `Send{ChatID, Text}`.
- **group:** respond only when the bot's `bot_open_id` is in the message `mentions` (Feishu also only pushes group messages to a bot when @mentioned by default — double safety). Reply: `Send{ChatID, Text, ReplyMessageID, Mentions:[sender]}` to thread under the original message.
- `ReplyTarget.Metadata` carries: `bot_id`, `chat_id`, `chat_type` (`p2p`/`group`), `message_id`. The ReplyGateway uses `chat_type` to choose the reply shape and `bot_id` to find the client.

### 4. Connection state & reconnect
The SDK reconnects internally. State mapping into the existing bot status machine:
- `OnReady` → `RuntimeStateConnected`
- ctx cancel / `Stop` → `ch.Stop` → `RuntimeStateStopped`
- fatal `ch.Start` error → `RuntimeStateError`

### 5. Bootstrap wiring (`internal/bootstrap/bootstrap.go`)
```go
feishuRegistry := feishu.NewRegistry()
feishuProvider := feishu.NewProvider(feishuRegistry, logger)
feishuReplyGateway := feishu.NewReplyGateway(feishuRegistry)
multiProvider.Register("feishu", feishuProvider, feishuProvider)
multiReplyGateway.Register("feishu", feishuReplyGateway)
```

### 6. Frontend (`internal/api/http/web/static/`)
- Add "飞书" to the channel-type selector in the create-bot modal.
- When channel type is `feishu`, render **App ID** and **App Secret** inputs.
- The connect action posts `app_id` / `app_secret` to `ConnectBot`.
- Because Feishu is auto-confirm, the connect response carries `connection_status` directly (no QR card / no polling), consistent with the existing HTTP-bot connect flow.

### 7. Testing
- Define the internal `feishuChannel` interface (`OnMessage`, `Send`, `Start`, `Stop`, `OnReady`) in `client.go`; the real SDK channel is one implementation.
- Tests inject a fake `feishuChannel` to drive provider / runtime / reply_gateway without a real wss, mirroring `wechat/fake_provider.go` and the wechat client interface seam.
- Cover: auto-confirm binding returns confirmed + credential payload; p2p message → OnEvent; group message without @ is ignored; group message with @ → OnEvent + reply uses `ReplyMessageID`; reply gateway looks up by `bot_id`; runtime Stop unregisters.

### 8. Dependency
Add `github.com/larksuite/oapi-sdk-go/v3` (official, High reputation) to `go.mod`.

## Risks / open implementation details
- **`NormalizedMessage` fields:** must expose `chat_type` and `mentions` for group @ detection. If it does not, switch the receive path (only) to low-level `larkws` + `EventDispatcher` on `im.message.receive_v1`, which exposes `Message.ChatType` and `Message.Mentions`; sending still uses `lark.Client`. The `feishuChannel` interface isolates this choice.
- **Concurrent connections per app:** with per-bot apps each bot opens its own wss; this is the intended model. If a single app is reused across bots, Feishu's per-app connection limit applies — out of scope, but note it for operators.
- **Permissions:** the self-built app needs `im:message` (send) and message-receive event subscription enabled in the Feishu developer console; document in the connect UI help text.
