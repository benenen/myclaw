# Root CLI Default Server Design

## Goal

Replace the direct `cmd/server` entrypoint with a single repository-root CLI in `main.go`. Running `go run .` should start the HTTP server by default, and `go run . server` should do the same explicitly.

## Context

The repository currently has two entrypoint locations:

- `cmd/server/main.go` contains the real server startup logic
- repository-root `main.go` is empty

That structure makes the service start command inconsistent with the desired workflow. The requested behavior is a single CLI entrypoint at the repository root, with `server` as both the explicit subcommand and the default action when no subcommand is provided.

## Scope

In scope:

- move server startup responsibility to repository-root `main.go`
- add minimal CLI command dispatch for `server`
- make no-argument invocation default to `server`
- add basic help and unknown-command handling
- remove `cmd/server` as a supported way to start the service
- update tests and README examples accordingly

Out of scope:

- introducing a third-party CLI framework
- adding more subcommands beyond `server` and `help`
- adding command-specific flags
- changing server configuration or runtime behavior

## Recommended Approach

Use a small standard-library CLI dispatcher in repository-root `main.go`.

Behavior:

- `go run .` runs `server`
- `go run . server` runs `server`
- `go run . help`, `go run . -h`, and `go run . --help` print usage text and exit successfully
- any other command prints an error plus usage text and exits with a non-zero status

This is the smallest change that satisfies the request while keeping room for future commands. A dedicated CLI framework would be unnecessary overhead for a single-command binary.

## Architecture

### 1. Root command dispatch

Repository-root `main.go` should become the only binary entrypoint.

Recommended structure:

```go
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
```

Recommended command handling:

```go
func run(args []string, stdout, stderr io.Writer) int
```

Responsibilities:

- normalize empty args to `server`
- route `server` to the existing server startup path
- route help aliases to usage output
- reject unknown commands with exit code `1`

Keeping command parsing in a pure `run(...)` helper makes behavior directly testable without spawning subprocesses.

### 2. Server startup extraction

Move the current startup logic from `cmd/server/main.go` into repository-root `main.go` helpers.

Recommended split:

```go
func runServer(stderr io.Writer) int
func serviceURL(addr string) string
```

`runServer` should keep the current behavior:

- load config
- bootstrap the app
- log the service URL
- run `http.ListenAndServe`
- return non-zero on failure

The implementation should preserve the existing startup semantics and only change how the binary is entered.

### 3. Help and error output

Use minimal built-in usage text, for example:

```text
Usage:
  myclaw [server]
  myclaw help
```

Design rules:

- help text goes to stdout
- unknown-command errors go to stderr
- error output includes the rejected command and usage text

This keeps the CLI usable without introducing a richer help system.

### 4. Removal of `cmd/server`

`cmd/server/main.go` should be removed so the repository has one canonical startup path.

This avoids drift where one entrypoint changes and the other is forgotten. After the change, any run instructions should reference `go run .`.

## Data Flow

### Default invocation

1. operator runs `go run .`
2. root `main.go` receives no positional args
3. dispatcher rewrites the command selection to `server`
4. server startup logic loads config and boots the app
5. HTTP server starts listening

### Explicit invocation

1. operator runs `go run . server`
2. dispatcher matches `server`
3. server startup logic runs

### Help invocation

1. operator runs `go run . help`
2. dispatcher writes usage text
3. process exits `0`

### Invalid invocation

1. operator runs `go run . foo`
2. dispatcher reports unknown command `foo`
3. usage text is printed to stderr
4. process exits `1`

## Error Handling

| Scenario | Expected behavior |
|---|---|
| config load fails | print startup error and exit non-zero |
| bootstrap fails | print startup error and exit non-zero |
| HTTP server fails to start | print startup error and exit non-zero |
| unknown command | print CLI error and usage to stderr, exit `1` |
| help requested | print usage to stdout, exit `0` |

## Testing Strategy

Add root entrypoint tests that exercise command parsing without invoking a real server listener.

Coverage:

- empty args default to `server`
- explicit `server` command selects server path
- `help`, `-h`, `--help` return success and print usage
- unknown command returns failure and prints usage
- existing `serviceURL` tests remain valid

To keep tests deterministic, command dispatch should be tested separately from the blocking server startup call. The server path can be injected or wrapped so tests can observe whether it was selected.

## Files to Change

| File | Purpose |
|---|---|
| `main.go` | implement root CLI and server startup |
| `main_test.go` | add CLI dispatch tests |
| `cmd/server/main.go` | remove old entrypoint |
| `README.md` | update run command examples |

## Migration Notes

- local startup command becomes `go run .`
- explicit startup command becomes `go run . server`
- `go run ./cmd/server` is no longer supported
