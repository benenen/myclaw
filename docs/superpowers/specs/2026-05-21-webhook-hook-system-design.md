# Webhook Hook System Design

## Overview

Add a generic hook system that receives external webhook requests at `/hooks/{id}`,
routes them to platform-specific hook implementations, sends the data to an AI agent
for processing, and lets the agent directly execute operations via its built-in tools.

## Architecture

```
POST /hooks/{id}
  → HookManager routes to Hook implementation by {id}
    → If a specific Hook is registered:
      → Hook.Handle() validates, extracts data, returns prompt
    → If no Hook registered (passthrough mode):
      → Request body is used directly as prompt
    → HookManager looks up Bot by name matching {id}
    → HookManager sends prompt to agent via agent.Manager.Send()
    → Agent processes data and executes operations (tools, etc.)
    → Returns agent response as HTTP response
```

## Hook Interface

```go
type Hook interface {
    ID() string
    Handle(ctx context.Context, r *http.Request) (prompt string, err error)
}
```

- `ID()` — platform identifier matching the URL `{id}` segment
- `Handle()` — lightweight preprocessing: verify auth, parse body, return the prompt text to send to the agent

## Components

### `internal/hook/` package

- **`hook.go`** — `Hook` interface
- **`manager.go`** — `Manager` struct
  - `RegisterHook(hook Hook)` — register a platform hook
  - `HandleHook(w, r, platformID)` — the core HTTP handler

### Manager

```go
type Manager struct {
    hooks    map[string]Hook
    botRepo  BotRepository       // interface with GetByName
    resolver *botapp.BotCLIResolver
    executor *agent.Manager
}

type BotRepository interface {
    GetByName(ctx context.Context, name string) (domain.Bot, error)
}

func NewManager(botRepo BotRepository, resolver *botapp.BotCLIResolver, executor *agent.Manager) *Manager
func (m *Manager) RegisterHook(hook Hook)
func (m *Manager) HandleHook(w http.ResponseWriter, r *http.Request, platformID string)
```

- Dependencies injected via constructor
- `BotRepository` interface abstracts the lookup — existing `BotRepository` will need a `GetByName` method
- `HandleHook` is the HTTP handler entry point, takes `platformID` as an explicit param
- **Dispatch logic:** If a Hook is registered for `platformID`, it is called to validate and extract the prompt. Otherwise the request body is read directly (passthrough mode). This means creating a Bot with a matching name is enough for `/hooks/{id}` to work — no Go code required.
- On webhook request:
  1. Look up the Hook by `platformID` in the registry
  2. If found: call `Hook.Handle()` to get prompt, return 400 on validation failure
  3. If not found (passthrough): read request body as prompt
  4. Look up Bot by `botRepo.GetByName(platformID)`, return 404 if not found  
  5. Resolve agent spec via `resolver.Resolve(ctx, bot.ID)`
  6. Call `executor.Send(ctx, bot.ID, spec, agent.Request{Prompt: prompt})` which returns `(agent.Response, error)` — `Response` has `Text`, `RuntimeType`, `ExitCode`, `Duration`, `RawOutput` fields
  7. Return `agent.Response.Text` as HTTP 200, body wrapped in existing `Envelope` format

### Bot Mapping

Bot name matches the platform ID in the URL. For example:
- Bot named `vikunja` → `POST /hooks/vikunja`
- Bot named `gitlab` → `POST /hooks/gitlab`

### Error Handling

- No bot found for `{id}` → HTTP 404 (whether hook registered or passthrough)
- Hook validation fails (bad auth, bad data) → HTTP 400 with error details
- Agent call fails → HTTP 502
- Agent succeeds → HTTP 200 with agent response text

### Passthrough Mode

If no `Hook` is registered for a platform ID, the Manager falls back to passthrough mode:
- Reads the raw request body
- Looks up a Bot whose name matches the platform ID
- Sends the body content as-is to the agent as a prompt

This means **creating a Bot named `vikunja` is all that's needed** for `/hooks/vikunja` to work.
Platforms that need signature validation or data transformation can still implement the `Hook` interface.

## Route Registration

Add to `handlers/router.go`:
```go
mux.Handle("POST /hooks/{id}", wrap(func(w http.ResponseWriter, r *http.Request) {
    hookManager.HandleHook(w, r, r.PathValue("id"))
}))
```

Note: the user requested "不限方法" (any HTTP method), but initially only POST will be registered.
Additional methods can be added per-platform if needed.

## Implementation Order

1. Add `GetByName(ctx, name) (domain.Bot, error)` to `BotRepository` interface and its implementation
2. Create `internal/hook/` package with `Hook` interface and `Manager`
3. Add route to `handlers/router.go`
4. Wire in `bootstrap/bootstrap.go`
5. Specific platform hooks (e.g., vikunja) are out of scope — the framework supports adding them later by implementing the `Hook` interface

## Testing

- Unit test for Hook registration and dispatch (mock `BotRepository` and `agent.Manager`)
- Test error paths: unknown platform, validation failure, bot not found, agent failure
- No helper subprocess needed — mock-based tests are sufficient for Manager logic
