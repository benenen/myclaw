# Channel Management Service MVP Design

**Date:** 2026-04-03
**Status:** Draft
**Scope:** `Hertz` HTTP service for centralized channel account management, first channel is `wechat`

## Problem

The repository currently has no service implementation. The first deliverable is a centralized HTTP service that manages channel-related information for multiple users, with WeChat as the first supported channel.

This service must solve four concrete problems:

1. Store multi-user channel authentication and configuration data centrally.
2. Support a service-owned WeChat binding flow, including QR code login and binding status tracking.
3. Generate an `app_key` for a bound channel account so downstream plugins can fetch runtime configuration.
4. Keep the service boundary narrow: the management service stores bindings and serves configuration, while the plugin/runtime uses `app_key` to fetch configuration and then starts its own MCP or AI CLI integration.

## Goals

- Build a Hertz-based HTTP service with only `GET` and `POST` endpoints.
- Support multiple platform users, where each `user_id` can bind multiple channel accounts over time.
- Model channel accounts generically so future channels such as `qq` and `feishu` can be added without renaming core tables or services.
- Implement WeChat as the first channel provider.
- Allow the service to initiate and manage the WeChat QR code binding flow.
- Generate, rotate, and disable a single active `app_key` per bound channel account.
- Let plugin/runtime fetch minimal runtime configuration by `app_key`.

## Non-Goals

- No full user login or identity system in this service for MVP.
- No unified message proxy, message relay, or runtime orchestration inside the service.
- No plugin heartbeat, plugin status callback, or centralized AI CLI process management in this spec.
- No multi-instance deployment design; SQLite is the MVP storage.
- No channel implementations beyond WeChat in this phase.

## Context And Assumptions

- Callers are trusted internal systems for MVP. They pass `user_id` to the management API.
- The service persists credentials and configuration centrally.
- The plugin/runtime does not persist authoritative channel configuration. It fetches configuration from this service using `app_key`.
- The plugin/runtime is responsible for starting MCP, Codex, CC, or other AI CLI processes after configuration fetch.
- WeChat login behavior will be adapted from the reference implementation shape in `m1heng/claude-plugin-weixin`, but the external API and storage model in this service remain service-owned and channel-agnostic.

## Recommended Architecture

The recommended approach is a configuration-center architecture.

The service owns:

- channel binding initiation and status tracking
- credential storage
- `app_key` issuance and validation
- runtime configuration assembly

The plugin/runtime owns:

- configuration fetch by `app_key`
- WeChat session startup using returned credentials
- MCP and AI CLI startup
- actual message transport between channel and AI runtime

This keeps the MVP bounded and avoids prematurely centralizing message flow or runtime lifecycle management.

## High-Level Components

### 1. HTTP API Layer

Built with Hertz. It exposes two API groups:

- management APIs for channel binding, account listing, and `app_key` operations
- runtime API for configuration fetch by `app_key`

The API style is action-oriented and restricted to `GET` and `POST`.

### 2. Application Service Layer

This layer contains domain rules and orchestration:

- create a channel binding session
- advance binding status
- create or rotate an `app_key`
- disable an `app_key`
- resolve runtime configuration by `app_key`

This layer should not depend on Hertz request types.

### 3. Channel Provider Layer

Introduce a provider interface such as:

```go
type Provider interface {
    CreateBinding(ctx context.Context, req CreateBindingRequest) (CreateBindingResult, error)
    RefreshBinding(ctx context.Context, req RefreshBindingRequest) (RefreshBindingResult, error)
    BuildRuntimeConfig(ctx context.Context, account ChannelAccount) (RuntimeConfig, error)
}
```

The first implementation is `wechat`. This isolates channel-specific login behavior and credential shapes from the rest of the service.

### 4. Storage Layer

SQLite-backed repositories store users, channel accounts, binding sessions, and `app_key` records.

Storage access should be hidden behind repositories so the service can move off SQLite later without changing domain logic. The schema should avoid SQLite-only behavior.

### 5. Security Layer

This layer handles:

- credential encryption and decryption
- `app_key` generation and hashing
- provider-specific secret serialization

## Domain Model

### `users`

Represents the service-local projection of an upstream platform user.

Suggested fields:

- `id`
- `external_user_id`
- `status`
- `created_at`
- `updated_at`

`external_user_id` is the upstream user identity passed by trusted callers.

### `channel_accounts`

Represents one bound channel account instance under one user.

Suggested fields:

- `id`
- `user_id`
- `channel_type`
- `account_uid`
- `display_name`
- `avatar_url`
- `bind_status`
- `credential_ciphertext`
- `credential_version`
- `last_bound_at`
- `created_at`
- `updated_at`

Constraints:

- unique key on `user_id + channel_type + account_uid`
- `channel_type` initially supports only `wechat`

For WeChat, `account_uid` stores the stable account identifier obtained from the provider.

### `channel_bindings`

Represents one binding session, not the durable account itself.

Suggested fields:

- `id`
- `user_id`
- `channel_type`
- `status`
- `provider_binding_ref`
- `qr_code_payload`
- `expires_at`
- `error_message`
- `channel_account_id`
- `created_at`
- `updated_at`
- `finished_at`

Suggested statuses:

- `pending`
- `qr_ready`
- `confirmed`
- `failed`
- `expired`

This table is required because QR-code login is asynchronous and a single channel account may go through multiple binding sessions over time.

### `app_keys`

Represents runtime access credentials for plugin/runtime fetch.

Suggested fields:

- `id`
- `user_id`
- `channel_account_id`
- `app_key_hash`
- `app_key_prefix`
- `status`
- `last_used_at`
- `created_at`
- `disabled_at`

Rules:

- only one active `app_key` per `channel_account_id` in MVP
- plaintext `app_key` is returned only once at creation time
- the database stores only hash and short prefix

## API Design

All APIs are under `/api/v1` and use only `GET` and `POST`.

### 1. Create Binding Session

`POST /api/v1/channel-bindings/create`

Request body:

```json
{
  "user_id": "u_123",
  "channel_type": "wechat"
}
```

Response body:

```json
{
  "code": "OK",
  "message": "",
  "request_id": "req_xxx",
  "data": {
    "binding_id": "bind_xxx",
    "status": "qr_ready",
    "qr_code_payload": "weixin://...",
    "expires_at": "2026-04-03T12:00:00Z"
  }
}
```

### 2. Get Binding Session Detail

`GET /api/v1/channel-bindings/detail?binding_id=bind_xxx`

Response data:

- `binding_id`
- `status`
- `channel_account_id`
- `channel_type`
- `display_name`
- `account_uid`
- `expires_at`
- `error_message`

The caller polls this endpoint until the session becomes `confirmed`, `failed`, or `expired`.

### 3. List Channel Accounts

`GET /api/v1/channel-accounts/list?user_id=u_123&channel_type=wechat`

Response data is a list of channel accounts with minimal management metadata, including whether an active `app_key` exists.

### 4. Create Or Rotate App Key

`POST /api/v1/channel-accounts/app-key/create`

Request body:

```json
{
  "channel_account_id": "acct_xxx"
}
```

Behavior:

- if an active key exists, disable it first
- create a new key
- return plaintext key only in this response

Response data:

- `key_id`
- `app_key`
- `app_key_prefix`
- `created_at`

### 5. Disable App Key

`POST /api/v1/channel-accounts/app-key/disable`

Request body supports either `channel_account_id` or `key_id`.

This keeps the API within the `GET`/`POST` constraint while still allowing explicit key revocation.

### 6. Fetch Runtime Configuration

`GET /api/v1/runtime/config`

Recommended authentication transport:

- request header: `X-App-Key`

Response data:

- `channel_type`
- `channel_account_id`
- `account_uid`
- `credential_blob`
- `runtime_options`

`credential_blob` is the minimal provider-specific credential package the plugin needs to start its own WeChat runtime. Internal management fields must not be returned.

## Binding Flow

1. Caller creates a channel binding session.
2. The application service asks the `wechat` provider to initialize a binding flow.
3. The provider returns QR payload and provider-side binding reference.
4. The service persists a `channel_bindings` row with `qr_ready`.
5. The caller displays the QR code and polls binding detail.
6. On each poll, the service may refresh provider-side state before returning the latest binding status.
7. When login is confirmed, the service upserts `channel_accounts`, encrypts and stores credentials, links the binding session to the account, and marks the session `confirmed`.
8. A management caller creates an `app_key` for the account.
9. The plugin/runtime calls runtime config fetch with `X-App-Key`.
10. The service validates the key hash, loads the channel account, decrypts credentials, and returns runtime configuration.

## Error Handling

Use a consistent response envelope:

```json
{
  "code": "APP_KEY_DISABLED",
  "message": "app key is disabled",
  "request_id": "req_xxx",
  "data": {}
}
```

Minimum business error codes:

- `OK`
- `INVALID_ARGUMENT`
- `NOT_FOUND`
- `BINDING_EXPIRED`
- `BINDING_FAILED`
- `APP_KEY_NOT_FOUND`
- `APP_KEY_DISABLED`
- `INTERNAL_ERROR`

Do not overload transport-level 500 errors for expected business states such as expired QR code or disabled key.

## Security Requirements

- Encrypt `credential_ciphertext` at rest using a service-managed master key.
- Hash `app_key` before storage. Never persist plaintext keys.
- Return plaintext `app_key` only once during creation.
- Keep runtime config responses minimal and provider-specific.
- Reserve an authentication middleware boundary for future upstream identity integration on management APIs.
- Record `last_used_at` on successful runtime config access.

## Testing Strategy

### Unit Tests

Focus on application services:

- create binding session
- binding state transitions
- upsert channel account after successful confirmation
- create or rotate `app_key`
- disable `app_key`
- resolve runtime config for valid and invalid keys

### Integration Tests

Focus on HTTP handlers plus SQLite:

- create binding session
- query binding detail
- list accounts
- create key
- disable key
- fetch runtime config

### Provider Tests

The WeChat provider should be behind an interface. CI should use a fake provider for deterministic tests. Real WeChat login should not be required in automated tests.

## Implementation Notes

- The repository is new, so structure should start cleanly with:
  - `cmd/server`
  - `internal/api/http`
  - `internal/app`
  - `internal/domain`
  - `internal/store`
  - `internal/channel/wechat`
  - `internal/security`
- The service should keep HTTP DTOs separate from domain entities.
- SQLite is acceptable for MVP, but migrations should be used from the start.

## Deferred Work

These items are intentionally out of scope for this spec and should be considered only after the MVP is running:

- plugin heartbeat and status callbacks
- centralized message relay
- multi-channel implementations beyond WeChat
- full user authentication and self-service login
- database migration to MySQL or PostgreSQL
- audit logs and operational dashboards

## Design Summary

The MVP is a Hertz-based centralized channel management service. It supports trusted internal callers, stores channel accounts in a channel-agnostic model, manages WeChat QR-code binding sessions, issues a single active `app_key` per channel account, and serves minimal runtime configuration to plugins. The plugin/runtime remains responsible for starting MCP, AI CLI processes, and the actual channel message loop.
