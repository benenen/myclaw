# Driver Init Registry Design

## Goal
Refactor agent driver execution so each bot initializes its selected driver once, then reuses that initialized runtime for request execution. Add a driver registry so new CLI drivers or model API adapters can register themselves and be selected by `Spec.Type`.

## Scope
In scope:
- add a driver registry in `internal/agent`
- change driver contract from direct `Run(spec, req)` to `Init(spec) -> runtime.Run(req)`
- make `Init` run once per bot/session
- make `Manager` and `Session` resolve drivers by `Spec.Type`
- adapt the existing oneshot driver to the new model
- keep bootstrap as the place that chooses spec/type

Out of scope:
- changing bot persistence schema
- adding multiple concrete new drivers now
- changing orchestrator behavior
- changing reply routing/channel integration

## Recommended Approach
Introduce a two-stage driver contract:

1. `Driver.Init(ctx, spec)` creates a bot/session-scoped runtime.
2. `SessionRuntime.Run(ctx, req)` executes a request against that initialized runtime.

Back this with a global driver registry in `internal/agent`. Each concrete driver registers itself via `func init()`. Session creation resolves the driver from `spec.Type`, calls `Init` once, and stores the returned runtime inside the session. All later requests for that bot go through the stored runtime.

This keeps driver selection and composition clean:
- `internal/agent` owns execution abstraction
- `bootstrap` owns which `Spec.Type` to use
- future CLI/API adapters only add a new driver file and registration

## Architecture

### 1. Driver registry
Update `internal/agent/driver.go`.

Recommended interfaces:

```go
type Driver interface {
	Init(ctx context.Context, spec Spec) (SessionRuntime, error)
}

type SessionRuntime interface {
	Run(ctx context.Context, req Request) (Response, error)
}

type DriverFactory func() Driver
```

Recommended registry API:

```go
func RegisterDriver(name string, factory DriverFactory)
func MustRegisterDriver(name string, factory DriverFactory)
func LookupDriver(name string) (Driver, bool)
```

Registry rules:
- key is `Spec.Type`
- duplicate registration panics
- missing driver returns a normal runtime error during session creation

### 2. Session lifecycle
Update `internal/agent/session.go`.

Recommended shape:

```go
type Session struct {
	mu      sync.Mutex
	state   SessionState
	spec    Spec
	runtime SessionRuntime
}
```

Creation flow:
- resolve driver from registry using `spec.Type`
- call `driver.Init(ctx, spec)`
- store cloned spec and returned runtime
- set state to `ready`

Execution flow:
- `Send` uses the stored `runtime.Run(ctx, req)`
- `Send` no longer passes `Spec` to execution
- state transitions remain `ready -> busy -> ready/broken`

This makes `Init` truly per-bot, once per session.

### 3. Manager behavior
Update `internal/agent/manager.go`.

Manager should no longer hold a concrete driver instance.

Responsibilities:
- create session on first request
- recreate session if stored session is broken
- recreate session if incoming spec differs from stored spec
- otherwise reuse session

This preserves current bot-keyed session behavior while moving driver selection to registry lookup.

### 4. Oneshot driver adaptation
Refactor the existing oneshot driver.

Recommended split:

```go
type OneshotDriver struct{}

type OneshotRuntime struct {
	spec Spec
}
```

Flow:
- `OneshotDriver.Init(ctx, spec)` validates/clones spec and returns `*OneshotRuntime`
- `OneshotRuntime.Run(ctx, req)` performs the existing command execution logic using stored spec

Register in package init:

```go
func init() {
	MustRegisterDriver("oneshot", func() Driver {
		return NewOneshotDriver()
	})
}
```

Even though oneshot has no persistent subprocess, `Init` is still useful to:
- freeze spec once per bot
- validate config once
- preserve a uniform lifecycle across all drivers

### 5. Bootstrap responsibility
Bootstrap remains the composition point.

Current bootstrap should continue to build a single `agent.Spec` from config. The important change is that bootstrap no longer injects a concrete driver into `agent.Manager`; instead it just constructs the spec with a valid `Type`.

Recommended near-term behavior:
- set `Spec.Type = "oneshot"`
- keep all CLI command/args/workdir/env selection in bootstrap/config

This keeps future swaps localized:
- add `codex-cli` driver -> new registration + `Spec.Type`
- add `anthropic-api` driver -> new registration + `Spec.Type`
- orchestrator and connection manager remain unchanged

### 6. Future extensibility
This design is intentionally compatible with future per-bot driver selection.

Possible next seam later:

```go
type SpecResolver interface {
	Resolve(bot domain.Bot) (agent.Spec, error)
}
```

That can be added later without changing the driver contract.

## Data Flow
1. Bootstrap builds `agent.Spec{Type: "oneshot", ...}`.
2. Orchestrator asks manager to send a request for `botID`.
3. Manager checks for an existing session.
4. If none exists, or the session is broken, or spec changed:
   - lookup driver in registry using `spec.Type`
   - call `Init(ctx, spec)` once
   - create/store session with returned runtime
5. Session executes each request with `runtime.Run(ctx, req)`.
6. On execution error, session becomes `broken`.
7. Next request recreates session via `Init`.

## Error Handling

| Scenario | Expected behavior |
|---|---|
| `Spec.Type` not registered | session creation fails with clear error |
| driver `Init` fails | session is not created; request fails |
| `Run` fails | session becomes `broken`; later request can recreate |
| duplicate driver registration | panic during program init |
| spec changes for same bot | manager replaces session and re-runs `Init` |

## Testing Strategy

### Driver registry tests
- register and lookup driver
- duplicate registration panics
- unknown driver lookup fails cleanly

### Session tests
- session creation resolves driver by `Spec.Type`
- `Init` called once per new session
- repeated `Send` on same session does not call `Init` again
- `Run` errors mark session broken

### Manager tests
- first `Send` creates session via registry
- same spec reuses existing session
- broken session recreates and calls `Init` again
- changed spec recreates and calls `Init` again

### Oneshot driver tests
- `Init` returns runtime
- runtime `Run` preserves existing command execution behavior
- existing output/timeout tests continue to pass after refactor

## Files to Change

| File | Purpose |
|---|---|
| `internal/agent/driver.go` | add driver registry and new interfaces |
| `internal/agent/session.go` | store initialized runtime instead of raw driver |
| `internal/agent/manager.go` | stop owning concrete driver; resolve by spec type |
| `internal/agent/driver_oneshot.go` | split into driver init + runtime run |
| `internal/agent/*_test.go` | add registry/init/session reuse coverage |
| `internal/bootstrap/bootstrap.go` | keep selecting spec/type in composition layer |

## Decision Summary
Use a registry-backed two-stage driver lifecycle:
- `Driver.Init(ctx, spec)` once per bot/session
- `SessionRuntime.Run(ctx, req)` per request

This makes `Init` semantically correct for CLI environment setup, keeps bootstrap as the driver selection point, and lets future CLIs or model API adapters plug in by registration rather than by rewriting orchestrator or session logic.
