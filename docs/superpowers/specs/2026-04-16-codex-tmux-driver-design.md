# Codex tmux Driver Design

## Goal
Add `internal/agent/codex/driver_tmux.go` so a bot can initialize one long-lived Codex terminal session inside tmux, send requests into that pane, detect request completion reliably, and return the resulting text as an `agent.Response`.

## Scope
In scope:
- add a tmux-backed Codex driver implementation
- register it in the agent driver registry as `codex-tmux`
- run `Init` once per bot/session
- run `Run` once per request on the initialized tmux runtime
- use `github.com/GianlucaP106/gotmux` to create and control tmux resources
- detect request completion using begin/end markers plus prompt restoration
- align start/end lifecycle with Codex notify without making notify the sole completion signal
- mark the runtime broken on timeout, tmux failure, or boundary ambiguity
- add focused tests for parsing, state transitions, and tmux request handling

Out of scope:
- streaming partial output
- multiple concurrent requests on one runtime
- automatic recovery of half-broken tmux state
- replacing `codex-pty`
- generic tmux or terminal framework extraction
- multi-pane or multi-window orchestration
- persistent transcript/history storage

## Recommended Approach
Implement a Codex-specific tmux driver using the existing `Driver.Init(ctx, spec) -> SessionRuntime` lifecycle.

`Init` should create one tmux session and pane per bot/session, start Codex inside that pane, wait until the pane reaches a stable ready prompt, then return a session-scoped runtime. `Run` should send one request into that pane, bracket the request with unique begin/end markers, wait until the end marker appears and the prompt is restored, then return only the new output for that request.

Codex notify should be treated as lifecycle alignment for start/end behavior, not as the only completion signal. The hard boundary remains pane content inspection using begin/end markers plus prompt restoration.

On timeout, pane loss, tmux command failure, or boundary ambiguity, the runtime should become broken so the manager can recreate it later.

## Architecture

### 1. Driver and runtime split
Add `internal/agent/codex/driver_tmux.go`.

Recommended types:

```go
type TMUXDriver struct{}

type TMUXRuntime struct {
	mu    sync.Mutex
	runMu sync.Mutex

	state runtimeState

	server      *gotmux.Server
	session     *gotmux.Session
	window      *gotmux.Window
	pane        *gotmux.Pane
	sessionName string
	prompt      string
	readErr     error
}
```

Registration:

```go
func init() {
	agent.MustRegisterDriver("codex-tmux", func() agent.Driver {
		return NewTMUXDriver()
	})
}
```

Responsibilities:
- `TMUXDriver.Init(ctx, spec)` starts the Codex terminal inside tmux and returns a ready runtime
- `TMUXRuntime.Run(ctx, req)` executes one request using the existing tmux pane
- `TMUXRuntime.Close()` cleans up the tmux session when the runtime is discarded, if the session lifecycle needs explicit teardown

### 2. Init lifecycle
`Init(ctx, spec)` should:
1. validate `spec.Command` and relevant args/env/workdir
2. create a unique tmux session name for the bot/session runtime
3. create one tmux session with one pane using gotmux
4. start Codex in that pane
5. inject the minimal notify-related startup behavior needed to stay aligned with Codex start/end notifications
6. wait until the pane reaches a stable ready prompt
7. capture the prompt signature for later request completion detection
8. return a `TMUXRuntime`

`Init` failure conditions:
- tmux server/session/pane creation fails
- Codex cannot be started in the pane
- pane never reaches ready prompt
- startup timeout is reached
- pane disappears before ready

### 3. Run lifecycle
`Run(ctx, req)` should be strictly serialized by the runtime mutex.

Per-request flow:
1. confirm runtime is `ready`
2. generate a unique request id
3. generate `BEGIN_<id>` and `END_<id>` markers
4. capture the current pane state or offset baseline
5. send the begin marker into the pane
6. send the user request into the pane
7. send the end marker trigger path into the pane in a Codex-compatible way
8. poll `capture-pane` until the request is complete
9. slice only the output between begin/end markers
10. remove markers, prompt echoes, and prompt tail from the returned text
11. return `agent.Response`

Failure conditions during `Run`:
- pane send-keys fails
- pane capture fails
- pane/session/window no longer exists
- request timeout
- begin/end markers cannot be located safely
- prompt never returns after end marker
- output cannot be cut cleanly for the current request

On failure, runtime becomes broken.

### 4. Completion detection
Completion must not rely on notify or silence alone.

Recommended primary rule:
- request begin marker detected in captured pane content
- request end marker detected after the begin marker
- Codex prompt restored after that end marker

Recommended helper logic:
- strip ANSI control sequences before matching
- normalize `\r\n`/`\r` to `\n`
- ignore prompt-like text that appears inside ordinary output lines
- slice by the latest matching begin/end pair for the active request id
- require prompt restoration after the selected end marker before returning

Notify integration:
- startup and request lifecycle should remain compatible with Codex notify expectations
- begin/end lifecycle can annotate or align with notify hooks
- notify does not replace marker-based extraction or prompt restoration checks

### 5. Reading model
There is no PTY reader goroutine in the tmux version.

Instead, the runtime should:
- write input with tmux pane send operations
- read output by polling `capture-pane`
- normalize captured content before boundary matching
- treat capture failures or missing pane resources as terminal runtime failures

This keeps the tmux implementation aligned with tmux’s control model instead of forcing the PTY architecture onto it.

### 6. Runtime state model
Recommended runtime states:
- `starting`
- `ready`
- `running`
- `broken`

Transitions:
- `starting -> ready`
- `ready -> running -> ready`
- `starting/running/ready -> broken`

### 7. Codex-specific constraints
The first implementation should remain Codex-specific and not over-abstract tmux handling.

That means it is acceptable to:
- encode Codex prompt recognition rules in this driver
- use Codex-specific request formatting assumptions
- keep marker and prompt parsing local to the tmux driver
- defer any shared PTY/tmux abstraction until a second tmux-based driver exists

## Error Handling

| Scenario | Expected behavior |
|---|---|
| tmux session creation fails | `Init` returns error |
| Codex never becomes ready | `Init` returns timeout error |
| pane/session disappears | `Run` returns error and runtime becomes broken |
| send-keys/capture-pane fails | `Run` returns error and runtime becomes broken |
| request exceeds timeout | `Run` returns timeout error and runtime becomes broken |
| boundary cannot be determined | `Run` returns error and runtime becomes broken |
| prompt does not return after end marker | `Run` returns error and runtime becomes broken |

## Testing Strategy

### Parsing/unit tests
Add pure tests for helper logic:
- marker detection in captured pane text
- prompt detection on its own line
- output slicing between begin/end markers
- cleanup of prompt echoes and marker lines
- ambiguous marker handling

### Runtime tests with fake tmux interactions
Use deterministic test doubles or helper processes for tmux-facing behavior where possible.

Recommended behaviors to simulate:
- startup prompt emission
- request output + end marker + prompt
- delayed output
- missing end marker
- pane disappearance
- prompt-like text inside normal output

### tmux runtime tests
Add tests for:
- `Init` reaches ready state
- `Init` times out when prompt never appears
- successful single request
- two consecutive requests only return their own output
- timeout marks runtime broken
- missing pane marks runtime broken
- concurrent `Run` calls do not overlap

## Files to Change

| File | Purpose |
|---|---|
| `internal/agent/codex/driver_tmux.go` | Codex tmux driver implementation |
| `internal/agent/codex/driver_tmux_test.go` | tmux driver parsing/runtime tests |
| `internal/app/capability/discoverer.go` | add `codex-tmux` to Codex supported modes |
| `internal/bootstrap/bootstrap_test.go` | assert `codex-tmux` registration through bootstrap wiring |
| `internal/app/capability/discoverer_test.go` | update supported mode expectations |
| `internal/agent/session_test.go` | optional coverage that `codex-tmux` still obeys session serialization if needed |

## Decision Summary
Build a Codex-specific tmux driver where `Init` starts one long-lived tmux session per bot and `Run` reuses that pane for requests. Use gotmux for tmux control, use marker-plus-prompt rules as the hard completion boundary, align with Codex notify at start/end, and treat timeout or boundary ambiguity as a broken runtime. This adds tmux-backed Codex support without replacing the existing `codex-pty` implementation.
