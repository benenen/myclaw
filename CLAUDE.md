# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**channel-plugin** — Centralized channel management service for AI CLI channel plugins. Go 1.23, standard `net/http`, SQLite via GORM, AES-256-GCM credential encryption. Licensed under MIT.

## Build & Run

```bash
export CHANNEL_MASTER_KEY=$(openssl rand -base64 32)
go run ./cmd/server
```

## Test

```bash
go test ./...                           # all tests
go test ./internal/app/... -v           # single package
go test ./internal/app -run TestCreate  # single test
```

## Architecture

- `cmd/server/main.go` — entry point, loads config and starts HTTP server
- `internal/bootstrap/` — dependency assembly
- `internal/config/` — environment-backed configuration
- `internal/api/http/` — response envelope, request ID middleware
- `internal/api/http/handlers/` — HTTP handlers and route registration
- `internal/api/http/dto/` — request/response DTOs
- `internal/app/` — application services (binding, app key, runtime, account query)
- `internal/domain/` — domain entities, repository interfaces, errors, statuses, ID generation
- `internal/store/` — SQLite database open/migrate, GORM models, repository implementations
- `internal/security/` — AES-GCM encryption and app_key generation/hashing
- `internal/channel/` — provider interface; `wechat/` has real + fake implementations
- `internal/testutil/` — shared test helpers (in-memory SQLite, HTTP test utils)

## Key Patterns

- All HTTP responses use `Envelope{Code, Message, RequestID, Data}` — even errors return HTTP 200 with business error codes
- External user IDs are resolved to internal `users.id` before any DB operations
- Provider interface (`channel.Provider`) abstracts channel-specific behavior; `FakeProvider` for deterministic tests
- Credentials encrypted at rest with AES-256-GCM; `app_key` hashed with SHA-256
- Repository layer uses GORM; migrations via embedded SQL files
