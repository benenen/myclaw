# Agent Capability Discovery Design

## Goal

At application startup, detect supported local CLI tools available on the machine, persist them as a global capability list, and let each bot choose both a capability and an execution mode (`oneshot` or `session`).

## Context

The current system wires a single global `agent.Spec` in `internal/bootstrap/bootstrap.go` and applies it to every bot. That prevents per-bot CLI selection and assumes the machine-level runtime command is fixed for the whole service.

The requested behavior is different:

- machine capabilities are global
- users select which capability each bot uses
- users also choose how that bot uses the capability (`oneshot` or `session`)

## Requirements

### Functional

1. On startup, the service scans the local machine for supported CLI commands.
2. Discovered commands are persisted in the database as a global machine capability list.
3. Each bot can select one capability from that list.
4. Each bot can select one execution mode supported by that capability.
5. Message handling resolves the selected capability per bot at runtime instead of using one global command for all bots.
6. The admin UI can display available capabilities and allow selecting a capability and mode for each bot.

### Non-functional

1. Startup scanning must not block service startup if detection fails.
2. Capability detection must be deterministic and conservative.
3. The first version should avoid free-form command execution configuration in the UI.
4. The design should leave room for future capability expansion without reworking bot configuration storage.

## Scope

### In scope

- startup scanning of a fixed whitelist of supported CLI commands
- persistence for global agent capabilities
- per-bot capability selection
- per-bot execution mode selection
- runtime resolution from bot configuration to `agent.Spec`
- HTTP APIs and web UI updates required for selection

### Out of scope

- scanning arbitrary executables from `PATH`
- user-defined custom commands in the UI
- version probing and compatibility negotiation
- advanced argument templating
- full protocol-level long-lived interactive session support beyond storing and routing a `session` mode selection

## Proposed Data Model

### Global capability table

Add a new global table: `agent_capabilities`

| Field | Type | Notes |
|---|---|---|
| `id` | string | prefixed primary key |
| `key` | string | stable identifier such as `codex` or `claude` |
| `label` | string | UI display name |
| `command` | string | executable name to launch |
| `args_json` | text/json | default args for this capability |
| `supported_modes_json` | text/json | supported execution modes |
| `available` | bool | whether the capability is currently detected on this machine |
| `detection_source` | string | e.g. `path_scan`, `seeded` |
| `last_detected_at` | timestamp nullable | updated when detection succeeds |
| `created_at` | timestamp | standard metadata |
| `updated_at` | timestamp | standard metadata |

### Bot configuration fields

Extend `bots` with:

| Field | Type | Notes |
|---|---|---|
| `agent_capability_id` | string | nullable foreign key to `agent_capabilities.id` |
| `agent_mode` | string | nullable, allowed values `oneshot` or `session` |

### Rationale

This separates two concerns cleanly:

| Concern | Stored where |
|---|---|
| what the machine can run | `agent_capabilities` |
| what a given bot should use | `bots.agent_capability_id`, `bots.agent_mode` |

That prevents command duplication across bots and supports future global rescans without overwriting each bot’s intent.

## Detection Model

### Capability seeds

The first version uses a fixed internal whitelist:

| Key | Label | Command | Supported modes |
|---|---|---|---|
| `codex` | Codex CLI | `codex` | `oneshot`, `session` |
| `claude` | Claude Code | `claude` | `oneshot`, `session` |

### Startup scan flow

At bootstrap:

1. load built-in capability seeds
2. for each seed, use `exec.LookPath(command)`
3. upsert the capability row
4. if detected:
   - set `available=true`
   - store the resolved command name/path used by the service
   - set `last_detected_at`
5. if not detected:
   - keep the row
   - set `available=false`

### Why whitelist-only

Scanning arbitrary executables from `PATH` would create noisy or unsafe data and would not provide enough metadata to know whether an executable is actually supported by this application. A whitelist keeps the detection deterministic and explainable.

## Runtime Architecture

## Current state

The app currently creates one global `agent.Spec` during bootstrap and injects it into the orchestrator.

## Target state

Resolve execution settings per bot at runtime.

### New responsibilities

| Component | Responsibility |
|---|---|
| `AgentCapabilityRepository` | persist and query global capability records |
| `AgentCapabilityScanner` | scan the local machine and upsert availability state |
| `BotAgentResolver` | load bot + capability and translate them to `agent.Spec` |
| `BotMessageOrchestrator` | request a spec per bot instead of storing one static spec |
| `agent.Driver` / `agent.Manager` | continue executing the chosen spec |

### Runtime flow

1. a bot receives a message
2. the orchestrator accepts the inbound event
3. before invoking the agent manager, it resolves the bot’s configured capability and mode
4. if resolution succeeds, the resolver returns an `agent.Spec`
5. the manager executes the request using that resolved spec
6. if resolution fails, the bot receives a fixed failure reply and the error is logged

## Mode Semantics

### `oneshot`

`oneshot` keeps current behavior: one request triggers one CLI invocation using the resolved command and args.

### `session`

The first version stores and routes a bot as `session`, but does not require a new protocol design in this feature. It should map onto the existing session-aware manager lifecycle so the configuration model is ready, while deeper interactive session semantics can be implemented later.

### Validation rules

| Rule | Behavior |
|---|---|
| bot has no capability selected | configuration error |
| capability does not exist | configuration error |
| capability is unavailable | configuration error |
| selected mode not in supported modes | configuration error |

## HTTP API

### List capabilities

`GET /agent-capabilities`

Response shape:

```json
[
  {
    "id": "cap_...",
    "key": "codex",
    "label": "Codex CLI",
    "available": true,
    "supported_modes": ["oneshot", "session"]
  }
]
```

### Configure bot agent

Preferred API:

`POST /bots/configure-agent`

Request:

```json
{
  "bot_id": "bot_...",
  "agent_capability_id": "cap_...",
  "agent_mode": "oneshot"
}
```

Response returns the updated bot agent configuration so the UI can re-render immediately.

## UI Design

### Create bot flow

The create-bot modal should include:

- CLI capability select
- execution mode select

Behavior:

1. load capabilities from the server
2. only show or enable currently available capabilities
3. after capability selection, populate mode options from `supported_modes`
4. submit selected capability and mode with bot creation or as an immediate follow-up configuration request

### Bot detail view

Display:

- selected capability label
- selected mode
- availability warning if the selected capability is no longer available on this machine

Provide an action to update bot agent settings without recreating the bot.

## Error Handling

| Situation | Handling |
|---|---|
| startup scan errors | log and continue startup |
| no capability detected on machine | UI shows empty-state guidance |
| bot has no configured capability | do not invoke CLI; reply with fixed configuration message |
| capability became unavailable after selection | reject execution and surface warning |
| invalid mode for capability | reject configuration request |

A missing or invalid bot capability should fail loudly in logs and predictably in the user-facing reply path. It should not silently drop messages.

## Testing Strategy

### Repository tests

- upsert capability rows
- list capabilities in stable order
- update availability state without duplicating rows

### Scanner tests

- detected command marks capability available
- missing command marks capability unavailable
- scan updates timestamps and source fields correctly

### Resolver tests

- configured bot resolves to the expected `agent.Spec`
- unavailable capability returns an error
- unsupported mode returns an error

### Orchestrator tests

- two bots can resolve different capabilities
- per-bot mode changes alter the resolved spec
- missing configuration returns a predictable reply

### HTTP tests

- capability list endpoint returns expected payload
- bot configuration endpoint validates inputs and persists changes

### Web tests

- create-bot modal loads capability options
- mode options depend on selected capability
- unavailable capability cannot be submitted

## Migration and Rollout

1. add schema for `agent_capabilities`
2. add bot columns for capability selection and mode
3. run startup scanner during bootstrap after DB setup
4. expose capability list API
5. expose bot configuration API
6. update UI to select and edit capability + mode
7. switch orchestrator from static global spec to resolver-based per-bot spec

## Recommended Initial Constraints

To keep the first version small and reliable:

- only support the built-in whitelist of `codex` and `claude`
- only store default args from code, not from user input
- do not build a custom command editor yet
- keep `session` as a valid persisted mode and runtime routing input, but avoid expanding protocol complexity in this change

## Open Decisions Resolved

| Topic | Decision |
|---|---|
| capability scope | global machine capability list |
| bot selection | each bot chooses one capability |
| mode selection | each bot chooses one supported mode |
| discovery scope | fixed whitelist only |
| persistence | store both capability inventory and bot selection in DB |

## Recommended Approach

Implement the feature as a global capability inventory plus per-bot configuration, and refactor runtime agent selection to resolve from bot state at message handling time. This matches the requested UX, fits the current architecture, and minimizes future migration pain when more CLI capabilities or richer session behavior are added.