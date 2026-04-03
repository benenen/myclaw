# Channel Management Service MVP Design

**Date:** 2026-04-03
**Status:** Approved
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
- API-level `user_id` always means the upstream external user identifier. Internally, this maps to `users.external_user_id`. Database foreign keys such as `channel_accounts.user_id` and `channel_bindings.user_id` reference the service-local `users.id`.
- The service persists credentials and configuration centrally.
- The plugin/runtime does not persist authoritative channel configuration. It fetches configuration from this service using `app_key`.
- The plugin/runtime is responsible for starting MCP, Codex, CC, or other AI CLI processes after configuration fetch.
- WeChat login behavior will be adapted from the reference implementation shape in `m1heng/claude-plugin-weixin`, but the external API and storage model in this service remain service-owned and channel-agnostic.
- All externally visible resource IDs such as `bind_xxx`, `acct_xxx`, and `key_xxx` should use a sortable opaque ID format such as `ULID`.

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
    BuildRuntimeConfig(ctx context.Context, req BuildRuntimeConfigRequest) (RuntimeConfig, error)
}
```

The first implementation is `wechat`. This isolates channel-specific login behavior and credential shapes from the rest of the service.

Minimal provider DTO semantics for MVP:

```go
type CreateBindingRequest struct {
    BindingID   string
    ChannelType string
}

type CreateBindingResult struct {
    ProviderBindingRef string
    QRCodePayload      string
    ExpiresAt          time.Time
}

type RefreshBindingRequest struct {
    ProviderBindingRef string
}

type RefreshBindingResult struct {
    ProviderStatus    string
    QRCodePayload     string
    ExpiresAt         time.Time
    AccountUID        string
    DisplayName       string
    AvatarURL         string
    CredentialPayload []byte
    CredentialVersion int
    ErrorMessage      string
}

type BuildRuntimeConfigRequest struct {
    AccountUID        string
    CredentialPayload []byte
    CredentialVersion int
}
```

Rules:

- `ProviderBindingRef` is the provider-side binding handle stored in `channel_bindings.provider_binding_ref`
- `CreateBinding` must obtain the initial QR payload used by the create API response
- `RefreshBinding` returns the latest provider state and may also return updated QR payload or expiry time
- `BuildRuntimeConfig` consumes already decrypted provider credentials and never reads ciphertext directly

Provider status mapping for WeChat MVP:

| Provider status | Internal `channel_bindings.status` | Meaning |
|---|---|---|
| `qr_ready` | `qr_ready` | QR code is available and may be displayed |
| `confirmed` | `confirmed` | Login succeeded and credentials are ready to persist |
| `failed` | `failed` | Login attempt ended with a provider error |
| `expired` | `expired` | QR code or binding session is no longer valid |

`pending` remains an internal service-side transient state before the first successful provider response is written.

### 4. Storage Layer

SQLite-backed repositories store users, channel accounts, binding sessions, and `app_key` records.

Storage access should be hidden behind repositories so the service can move off SQLite later without changing domain logic. The schema should avoid SQLite-only behavior.

### 5. Security Layer

This layer handles:

- credential encryption and decryption
- `app_key` generation and hashing
- provider-specific secret serialization

Responsibility boundary:

- the security package encrypts and decrypts provider credential payloads
- the application service calls security code and passes plaintext credential payloads into provider DTOs
- channel providers never read ciphertext from storage and do not depend on the security package

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

Repository and application service logic must resolve API `user_id` input to `users.id` before creating or querying any related records.

### `channel_accounts`

Represents one bound channel account instance under one user.

Suggested fields:

- `id`
- `user_id`
- `channel_type`
- `account_uid`
- `display_name`
- `avatar_url`
- `credential_ciphertext`
- `credential_version`
- `last_bound_at`
- `created_at`
- `updated_at`

Constraints:

- unique key on `user_id + channel_type + account_uid`, where `user_id` here is the internal `users.id`
- `channel_type` initially supports only `wechat`

For WeChat, `account_uid` stores the stable account identifier obtained from the provider.

MVP note:

- `channel_accounts` does not store a separate `bind_status`
- current account usability is derived from whether a credential payload exists and whether the latest successful binding completed

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

State transition rules for MVP:

- newly created session starts at `pending`
- during `POST /channel-bindings/create`, once QR payload is ready, move to `qr_ready`
- on confirmed provider login, move to `confirmed`
- on provider-side login failure, move to `failed`
- on expiry time reached before confirmation, move to `expired`

Expiry handling in MVP is request-driven. The service evaluates expiry during binding-detail reads and any provider refresh operation. No background scheduler is required in this spec.

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
- `app_key_prefix` should be the first 8 visible characters for operator troubleshooting
- `app_key_hash` should use `SHA-256` over the full plaintext key
- plaintext `app_key` should be generated from at least 32 random bytes and encoded as URL-safe base64 without padding, optionally with a fixed prefix such as `appk_`
- disabling by `channel_account_id` is idempotent; if no active key exists, return `OK`

## API Design

All APIs are under `/api/v1` and use only `GET` and `POST`.

All success and error responses use the same envelope:

```json
{
  "code": "OK",
  "message": "",
  "request_id": "req_xxx",
  "data": {}
}
```

### 1. Create Binding Session

`POST /api/v1/channel-bindings/create`

This endpoint is synchronous for QR initialization in MVP. The handler creates the binding record, calls provider `CreateBinding` inline, persists the returned QR payload and provider reference, and returns only after the binding is ready for polling. Successful responses therefore return `status = qr_ready`, not `pending`.

Request body:

```json
{
  "user_id": "u_123",
  "channel_type": "wechat"
}
```

Here `user_id` is the upstream external user identifier, not `users.id`.

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

If the provider cannot initialize the binding, the endpoint returns an error envelope and does not return a successful `pending` response.

### 2. Get Binding Session Detail

`GET /api/v1/channel-bindings/detail?binding_id=bind_xxx`

This endpoint is not a pure database read in MVP. Each call refreshes provider state using `provider_binding_ref`, persists any state change, and then returns the latest stored view. Polling clients should treat this endpoint as the authoritative refresh path.

For polling ergonomics, this endpoint always returns envelope `code = OK` when the binding exists, even if `data.status` is `failed` or `expired`. Callers should branch on `data.status` for terminal binding states.

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

Request body supports either `channel_account_id` or `key_id`, but not both. If both are provided, return `INVALID_ARGUMENT`.

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

`RuntimeConfig` for MVP maps to the HTTP response as:

- `credential_blob.version`: integer schema version for the runtime credential contract
- `credential_blob.payload`: provider-specific JSON object, not opaque base64
- `runtime_options`: JSON object for non-secret runtime hints such as poll intervals

For WeChat MVP, `BuildRuntimeConfig` returns a structure that can be serialized directly into this response shape.

Example success response:

```json
{
  "code": "OK",
  "message": "",
  "request_id": "req_xxx",
  "data": {
    "channel_type": "wechat",
    "channel_account_id": "acct_xxx",
    "account_uid": "wxid_xxx",
    "credential_blob": {
      "version": 1,
      "payload": {
        "wechat_session": {},
        "device": {}
      }
    },
    "runtime_options": {
      "poll_interval_seconds": 3
    }
  }
}
```

## Binding Flow

1. Caller creates a channel binding session.
2. The application service inserts a `channel_bindings` row in `pending`.
3. The application service calls `wechat.Provider.CreateBinding` inline.
4. The provider returns `provider_binding_ref`, QR payload, and expiry time.
5. The service updates the binding row to `qr_ready` and returns the create response with QR payload.
6. The caller displays the QR code and polls binding detail.
7. On each poll, the application service calls `wechat.Provider.RefreshBinding` with `provider_binding_ref`.
8. If provider status is still `qr_ready`, the service returns the current binding detail and may update QR payload or expiry time if the provider rotated them.
9. If provider status is `confirmed`, the service encrypts the returned `CredentialPayload`, upserts `channel_accounts`, links the binding session to the account, and marks the session `confirmed`.
10. If provider status is `failed` or `expired`, the service marks the binding session accordingly.
11. A management caller creates an `app_key` for the account.
12. The plugin/runtime calls runtime config fetch with `X-App-Key`.
13. The service validates the key hash, loads the channel account, decrypts credentials, passes plaintext provider credentials into `BuildRuntimeConfig`, and returns runtime configuration.

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

Endpoint-specific code rules for MVP:

| Endpoint | Scenario | Envelope `code` |
|---|---|---|
| `POST /channel-bindings/create` | validation error | `INVALID_ARGUMENT` |
| `POST /channel-bindings/create` | provider cannot initialize binding | `BINDING_FAILED` |
| `GET /channel-bindings/detail` | binding not found | `NOT_FOUND` |
| `GET /channel-bindings/detail` | binding exists and status is `qr_ready` / `confirmed` / `failed` / `expired` | `OK` |
| `POST /channel-accounts/app-key/create` | channel account not found | `NOT_FOUND` |
| `POST /channel-accounts/app-key/disable` | both identifiers provided | `INVALID_ARGUMENT` |
| `GET /runtime/config` | app key missing or unknown | `APP_KEY_NOT_FOUND` |
| `GET /runtime/config` | app key disabled | `APP_KEY_DISABLED` |

## Security Requirements

- Encrypt `credential_ciphertext` at rest using a service-managed master key loaded from environment or secret injection, for example `CHANNEL_MASTER_KEY`.
- Use an authenticated encryption primitive such as `AES-256-GCM`.
- Hash `app_key` before storage. Never persist plaintext keys.
- Return plaintext `app_key` only once during creation.
- Keep runtime config responses minimal and provider-specific.
- Reserve an authentication middleware boundary for future upstream identity integration on management APIs.
- Record `last_used_at` on successful runtime config access.
- `credential_version` refers to the serialized credential payload schema version, not the master-key rotation version.

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
