# Codex PTY Driver Design

## Goal
Add `internal/agent/codex/driver_pty.go` so a bot can initialize one interactive Codex terminal session via PTY, send requests through that PTY, wait for Codex to finish, and return the resulting text as an `agent.Response`.

## Scope
In scope:
- add a PTY-backed Codex driver implementation
- register it in the agent driver registry
- run `Init` once per bot/session
- run `Run` once per request on the initialized PTY runtime
- detect request completion using prompt/marker-based boundaries
- mark the runtime broken on timeout, PTY failure, or boundary loss
- add focused tests for parsing, state transitions, and interactive request handling

Out of scope:
- streaming partial output
- multiple concurrent requests on one PTY runtime
- generic PTY framework for every future driver
- persistent transcript/history storage
- automatic recovery of half-broken PTY state
- broad support for arbitrary terminal UIs beyond Codex

## Recommended Approach
Implement a Codex-specific PTY driver using the new `Driver.Init(ctx, spec) -> SessionRuntime` lifecycle.

`Init` should create one interactive Codex terminal per bot/session using PTY, wait until the terminal is in a stable ready state, then return a session-scoped runtime. `Run` should write a single request into that PTY, wait for Codex to finish by using a strict completion protocol, and return only the newly generated output for that request.

The first version should use a dual completion rule:
1. primary: marker detected and prompt restored
2. fallback: prompt restored and output has remained quiet for a short bounded window

On timeout, PTY read/write failure, or boundary ambiguity, the runtime should become broken so the manager can recreate it later.

## Architecture

### 1. Driver and runtime split
Add `internal/agent/codex/driver_pty.go`.

Recommended types:

```go
type PTYDriver struct{}

type PTYRuntime struct {
	mu sync.Mutex

	cmd *exec.Cmd
	pty *os.File

	state runtimeState
	prompt string

	notifyCh chan struct{}
	readErr error

	rawBuffer []byte
	normalized string
}
```

Registration:

```go
func init() {
	agent.MustRegisterDriver("codex-pty", func() agent.Driver {
		return NewPTYDriver()
	})
}
```

Responsibilities:
- `PTYDriver.Init(ctx, spec)` starts the Codex terminal and returns a ready runtime
- `PTYRuntime.Run(ctx, req)` executes one request using the existing PTY session

### 2. Init lifecycle
`Init(ctx, spec)` should:
1. validate `spec.Command` and relevant args/env/workdir
2. start Codex under PTY
3. start a background reader goroutine
4. normalize terminal output for matching logic
5. wait until the terminal reaches a stable ready state
6. capture the prompt signature for later request completion detection
7. return a `PTYRuntime`

`Init` failure conditions:
- PTY creation fails
- command start fails
- process exits before ready
- startup timeout is reached
- no stable ready prompt can be identified

### 3. Run lifecycle
`Run(ctx, req)` should be strictly serialized by the runtime mutex.

Per-request flow:
1. confirm runtime is `ready`
2. generate a unique request marker
3. record the current output offset
4. write the user request into PTY in Codex-compatible input form
5. write or otherwise induce the unique end marker for this request boundary
6. wait for completion:
   - marker seen and prompt restored, or
   - fallback: prompt restored and idle silence threshold reached
7. extract only the new output for this request
8. remove marker/prompt tail from the returned text
9. return `agent.Response`

Failure conditions during `Run`:
- PTY write failure
- PTY EOF/read error
- Codex process exit
- request timeout
- completion marker never appears and fallback does not safely trigger
- output cannot be cut cleanly for the current request

On failure, runtime becomes broken.

### 4. Completion detection
Completion must not rely on silence alone.

Recommended primary rule:
- request marker detected in normalized output
- prompt restored after that marker

Recommended fallback:
- prompt restored
- no new output for a short quiet period such as 300-800ms

Recommended helper logic:
- strip ANSI control sequences before matching
- normalize `\r\n`/`\r` to `\n`
- maintain both raw and normalized output views
- keep per-request offsets so only new output is returned

### 5. Reader goroutine
The background reader should only:
- read PTY continuously
- append raw bytes to buffer
- append normalized text to a searchable string/ring buffer
- store terminal read errors/EOF
- notify waiters when new output arrives

It should not decide request success or mutate high-level runtime state beyond recording terminal failure.

### 6. Runtime state model
Recommended runtime states:
- `starting`
- `ready`
- `running`
- `broken`
- `closing`

Transitions:
- `starting -> ready`
- `ready -> running -> ready`
- `starting/running/ready -> broken`
- optional `* -> closing` if a close path is added later

### 7. Codex-specific constraints
The first implementation should remain Codex-specific and not over-abstract PTY handling.

That means it is acceptable to:
- encode Codex prompt recognition rules in this driver
- use Codex-specific request formatting assumptions
- defer generic terminal-driver abstraction until a second PTY-based driver exists

## Error Handling

| Scenario | Expected behavior |
|---|---|
| PTY startup fails | `Init` returns error |
| Codex never becomes ready | `Init` returns timeout error |
| PTY read/write fails | `Run` returns error and runtime becomes broken |
| request exceeds timeout | `Run` returns timeout error and runtime becomes broken |
| boundary cannot be determined | `Run` returns error and runtime becomes broken |
| process exits unexpectedly | runtime becomes broken and future `Run` fails fast |

## Testing Strategy

### Parsing/unit tests
Add pure tests for helper logic:
- ANSI stripping
- prompt detection
- marker detection
- output slicing from a start offset
- fallback completion rule behavior

### Runtime tests with fake terminal
Use a deterministic fake interactive terminal program instead of real Codex for most tests.

Recommended behaviors to simulate:
- startup prompt emission
- request output + marker + prompt
- delayed output
- missing marker
- early process exit
- repeated empty output

### PTY runtime tests
Add tests for:
- `Init` reaches ready state
- `Init` times out on no prompt
- successful single request
- two consecutive requests only return their own output
- timeout marks runtime broken
- EOF marks runtime broken
- concurrent `Run` calls do not overlap

## Files to Change

| File | Purpose |
|---|---|
| `internal/agent/codex/driver_pty.go` | Codex PTY driver implementation |
| `internal/agent/codex/driver_pty_test.go` | PTY driver parsing/runtime tests |
| `internal/agent/driver.go` | no behavior change beyond using existing registry if already present |
| `internal/agent/session_test.go` | optional coverage that `codex-pty` still obeys session serialization if needed |

## Decision Summary
Build a Codex-specific PTY driver where `Init` starts one interactive terminal per bot and `Run` reuses that initialized session for requests. Detect completion using marker-plus-prompt rules with a bounded fallback, and treat timeout or boundary ambiguity as a broken runtime. This gives the desired interactive terminal behavior while staying compatible with the existing driver registry and per-bot session lifecycle.
