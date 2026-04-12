# Repository Guidelines

This repository uses `AGENTS.md` as the canonical contributor and agent guide. `CLAUDE.md` may point to this file so the instructions stay in one place.

## Project Overview

`myclaw` is a Go 1.23 service for centralized channel management for AI CLI plugins. It uses standard `net/http`, SQLite via GORM, and AES-256-GCM for credential encryption.

## Project Structure & Module Organization

`cmd/server` contains the HTTP entrypoint. Core code lives in `internal/`: `api/http` for handlers and DTOs, `app` for services, `bootstrap` for wiring, `channel` for provider integrations, `config` for env-backed settings, `domain` for entities and repository interfaces, `security` for encryption helpers, `store` for SQLite/GORM persistence, and `testutil` for shared test helpers. Notes and plans live in `docs/superpowers/`.

## Build, Test, and Development Commands

- `export CHANNEL_MASTER_KEY=$(openssl rand -base64 32)` prepares a local master key.
- `go run ./cmd/server` starts the server locally.
- `go build ./cmd/server` verifies the server binary compiles.
- `go test ./...` runs the full test suite.
- `go test ./internal/app -run TestCreate` runs a focused test.
- `make run` starts the app with Makefile defaults.
- `make watch` runs live reload via `air`.

## Coding Style & Naming Conventions

Use standard Go formatting with tabs and `gofmt`. Keep packages cohesive and follow existing naming like `NewBotService`, `ChannelBindingRepository`, and `CreateBotInput`. Exported identifiers use `PascalCase`; unexported identifiers use `camelCase`. Keep HTTP responses aligned with the existing `Envelope{Code, Message, RequestID, Data}` pattern.

## Testing Guidelines

Keep tests next to the code they cover in `_test.go` files. Prefer direct scenario tests unless table-driven tests make the case clearer. Reuse helpers from `internal/testutil` and in-memory SQLite where possible. Before finishing work, run the narrowest relevant test and then `go test ./...`.

## Commit & Pull Request Guidelines

Use short conventional commit prefixes consistent with recent history: `feat:`, `fix:`, `refactor:`, and `docs:`. Keep each commit scoped to one change. PRs should explain the problem, the approach, and the exact verification commands run. Call out API, schema, config, or UI changes explicitly.

## Security & Configuration Tips

Never commit real secrets or populated SQLite databases. Set `CHANNEL_MASTER_KEY` to a base64-encoded 32-byte value for local runs. Keep WeChat integration settings in environment variables rather than hardcoding them.
