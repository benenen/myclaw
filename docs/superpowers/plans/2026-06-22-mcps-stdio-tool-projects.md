# mcps/ stdio MCP-tool Projects Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Populate the empty `mcps/` folder with two independent Go modules (`echo`, `ping`), each compiling to its own stdio MCP-server binary, tied together by a root `go.work`.

**Architecture:** Each project is a nested Go module with its own `go.mod`, carved out of the main `github.com/benenen/myclaw` module. Each `main.go` builds an MCP server with `github.com/modelcontextprotocol/go-sdk/mcp`, registers one tool, and serves it over `&mcp.StdioTransport{}`. Tool logic lives in a pure function so it is unit-tested without a transport. A committed root `go.work` resolves the local modules; `Makefile` targets build and test them.

**Tech Stack:** Go 1.23.6, `github.com/modelcontextprotocol/go-sdk v0.8.0`, Go workspaces (`go.work`), Make.

**Spec:** `docs/superpowers/specs/2026-06-22-mcps-stdio-tool-projects-design.md`

---

## File Structure

| File | Responsibility |
|---|---|
| `mcps/echo/go.mod` | echo module definition (requires go-sdk v0.8.0) |
| `mcps/echo/echo.go` | pure tool logic + I/O types (`runEcho`, `EchoInput`, `EchoOutput`) |
| `mcps/echo/echo_test.go` | unit test for `runEcho` |
| `mcps/echo/main.go` | wire server + `echo` tool + stdio transport |
| `mcps/ping/go.mod` | ping module definition |
| `mcps/ping/ping.go` | pure tool logic + output type (`runPing`, `PingOutput`) |
| `mcps/ping/ping_test.go` | unit test for `runPing` |
| `mcps/ping/main.go` | wire server + `ping` tool + stdio transport |
| `go.work` | workspace listing `.`, `./mcps/echo`, `./mcps/ping` (committed) |
| `.gitignore` | track `go.work`, ignore `go.work.sum`, ignore `/bin/` |
| `Makefile` | `mcps` / `mcp-echo` / `mcp-ping` / `test-mcps` targets |

Modules are created **before** `go.work` (Task 3): a `go.work` that references a not-yet-existing module makes root-level `go` commands error.

---

### Task 1: echo module

**Files:**
- Create: `mcps/echo/echo.go`
- Create: `mcps/echo/echo_test.go`
- Create: `mcps/echo/main.go`
- Create: `mcps/echo/go.mod` (via `go mod init` + `go mod tidy`)

- [ ] **Step 1: Write the pure tool logic and I/O types**

Create `mcps/echo/echo.go`:

```go
package main

// EchoInput is the input schema for the echo tool.
type EchoInput struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

// EchoOutput is the output schema for the echo tool.
type EchoOutput struct {
	Text string `json:"text" jsonschema:"the echoed text"`
}

// runEcho is the pure tool logic, testable without a transport.
func runEcho(in EchoInput) EchoOutput {
	return EchoOutput{Text: in.Text}
}
```

- [ ] **Step 2: Write the failing test**

Create `mcps/echo/echo_test.go`:

```go
package main

import "testing"

func TestRunEcho(t *testing.T) {
	got := runEcho(EchoInput{Text: "hello"})
	if got.Text != "hello" {
		t.Fatalf("runEcho() = %q, want %q", got.Text, "hello")
	}
}
```

- [ ] **Step 3: Initialize the module**

Run:
```bash
cd mcps/echo && go mod init github.com/benenen/myclaw/mcps/echo && cd ../..
```
Expected: `mcps/echo/go.mod` created with `module github.com/benenen/myclaw/mcps/echo` and `go 1.23.6`.

- [ ] **Step 4: Write the server entrypoint**

Create `mcps/echo/main.go`:

```go
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Reflect the given text back to the caller.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EchoInput) (*mcp.CallToolResult, EchoOutput, error) {
		return nil, runEcho(in), nil
	})

	// stdio: diagnostics MUST go to stderr (log defaults to stderr); stdout is the JSON-RPC stream.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("echo mcp server: %v", err)
	}
}
```

- [ ] **Step 5: Resolve dependencies**

Run:
```bash
cd mcps/echo && go mod tidy && cd ../..
```
Expected: `go.mod` gains `require github.com/modelcontextprotocol/go-sdk v0.8.0` (plus transitive requires); `go.sum` created. All deps already live in the module cache (the main module uses the same SDK version), so this works offline.

- [ ] **Step 6: Run the test to verify it passes**

Run:
```bash
cd mcps/echo && go test ./... && cd ../..
```
Expected: `ok  github.com/benenen/myclaw/mcps/echo`.

- [ ] **Step 7: Verify it compiles**

Run:
```bash
cd mcps/echo && go build -o /dev/null . && cd ../..
```
Expected: no output, exit 0.

- [ ] **Step 8: Commit**

```bash
git add mcps/echo
git commit -m "feat(mcps): echo stdio MCP server"
```

---

### Task 2: ping module

**Files:**
- Create: `mcps/ping/ping.go`
- Create: `mcps/ping/ping_test.go`
- Create: `mcps/ping/main.go`
- Create: `mcps/ping/go.mod` (via `go mod init` + `go mod tidy`)

- [ ] **Step 1: Write the pure tool logic and output type**

Create `mcps/ping/ping.go`:

```go
package main

import "time"

// PingOutput is the output schema for the ping tool.
type PingOutput struct {
	Time    string `json:"time" jsonschema:"the server time in RFC3339"`
	Message string `json:"message" jsonschema:"always 'pong'"`
}

// runPing is the pure tool logic, testable without a transport.
func runPing() PingOutput {
	return PingOutput{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Message: "pong",
	}
}
```

- [ ] **Step 2: Write the failing test**

Create `mcps/ping/ping_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestRunPing(t *testing.T) {
	got := runPing()
	if got.Message != "pong" {
		t.Fatalf("runPing().Message = %q, want %q", got.Message, "pong")
	}
	if _, err := time.Parse(time.RFC3339, got.Time); err != nil {
		t.Fatalf("runPing().Time = %q, not RFC3339: %v", got.Time, err)
	}
}
```

- [ ] **Step 3: Initialize the module**

Run:
```bash
cd mcps/ping && go mod init github.com/benenen/myclaw/mcps/ping && cd ../..
```
Expected: `mcps/ping/go.mod` created with `module github.com/benenen/myclaw/mcps/ping` and `go 1.23.6`.

- [ ] **Step 4: Write the server entrypoint**

The ping tool takes no input, so the handler's input parameter is the empty struct `struct{}` (matching the `list_agents` tool in `internal/app/orchestration/mcp.go`).

Create `mcps/ping/main.go`:

```go
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "ping", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Return the current server time and 'pong'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, PingOutput, error) {
		return nil, runPing(), nil
	})

	// stdio: diagnostics MUST go to stderr (log defaults to stderr); stdout is the JSON-RPC stream.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("ping mcp server: %v", err)
	}
}
```

- [ ] **Step 5: Resolve dependencies**

Run:
```bash
cd mcps/ping && go mod tidy && cd ../..
```
Expected: `go.mod` gains `require github.com/modelcontextprotocol/go-sdk v0.8.0`; `go.sum` created.

- [ ] **Step 6: Run the test to verify it passes**

Run:
```bash
cd mcps/ping && go test ./... && cd ../..
```
Expected: `ok  github.com/benenen/myclaw/mcps/ping`.

- [ ] **Step 7: Verify it compiles**

Run:
```bash
cd mcps/ping && go build -o /dev/null . && cd ../..
```
Expected: no output, exit 0.

- [ ] **Step 8: Commit**

```bash
git add mcps/ping
git commit -m "feat(mcps): ping stdio MCP server"
```

---

### Task 3: Root go.work

**Files:**
- Create: `go.work`

- [ ] **Step 1: Create the workspace file**

Create `go.work` at the repo root:

```
go 1.23.6

use (
	.
	./mcps/echo
	./mcps/ping
)
```

- [ ] **Step 2: Verify the workspace resolves all modules**

Run from the repo root:
```bash
go build ./mcps/echo ./mcps/ping
```
Expected: no output, exit 0 (workspace mode resolves the nested modules by relative path).

- [ ] **Step 3: Verify the main module still builds under the workspace**

Run:
```bash
go build ./cmd/server
```
Expected: no output, exit 0 (adding `.` to the workspace must not disturb the main module).

- [ ] **Step 4: Commit**

```bash
git add go.work
git commit -m "build(mcps): go.work workspace tying mcps modules to main"
```

---

### Task 4: .gitignore — track go.work, ignore bin/

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Stop ignoring go.work but keep ignoring go.work.sum**

In `.gitignore`, replace this block:

```
# Go workspace file
go.work
go.work.sum
```

with:

```
# Go workspace file (go.work is committed; only the resolved sum is ignored)
go.work.sum
```

- [ ] **Step 2: Ignore built binaries**

In `.gitignore`, find the `# Binaries for programs and plugins` section:

```
# Binaries for programs and plugins
*.exe
*.exe~
*.dll
*.so
*.dylib
```

Add `/bin/` immediately below it:

```
# Binaries for programs and plugins
*.exe
*.exe~
*.dll
*.so
*.dylib
/bin/
```

- [ ] **Step 3: Verify go.work is now tracked and bin/ is ignored**

Run:
```bash
git check-ignore -v go.work; echo "go.work ignored? exit=$?"
git check-ignore -v go.work.sum bin/x
```
Expected: `go.work ignored? exit=1` (NOT ignored → tracked); `go.work.sum` and `bin/x` ARE printed (ignored).

- [ ] **Step 4: Commit**

```bash
git add .gitignore
git commit -m "build(mcps): track go.work, ignore go.work.sum and bin/"
```

---

### Task 5: Makefile targets

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Append build/test targets**

Append to `Makefile` (after the existing `watch` target):

```makefile

mcp-echo:
	go build -o bin/mcp-echo ./mcps/echo

mcp-ping:
	go build -o bin/mcp-ping ./mcps/ping

mcps: mcp-echo mcp-ping

test-mcps:
	go test ./mcps/echo/... ./mcps/ping/...

.PHONY: test run watch mcp-echo mcp-ping mcps test-mcps
```

- [ ] **Step 2: Build both binaries**

Run from the repo root:
```bash
make mcps
```
Expected: produces `bin/mcp-echo` and `bin/mcp-ping`, exit 0.

- [ ] **Step 3: Run the module tests via the target**

Run:
```bash
make test-mcps
```
Expected: `ok` for both `.../mcps/echo` and `.../mcps/ping`.

- [ ] **Step 4: Smoke-test the echo binary over stdio**

Run:
```bash
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | ./bin/mcp-echo
```
Expected: two JSON-RPC response lines on stdout; the second lists a tool named `echo`. (Process stays open on stdin EOF handling — if it does not exit, Ctrl-C is fine; the goal is to see the `tools/list` response.)

- [ ] **Step 5: Confirm the main suite is unaffected**

Run:
```bash
go test ./...
```
Expected: PASS (root `go test ./...` covers only the main module; nested modules are covered by `make test-mcps`).

- [ ] **Step 6: Commit**

```bash
git add Makefile
git commit -m "build(mcps): make targets to build and test mcp servers"
```

---

## Self-Review

**Spec coverage:**
- stdio transport → Tasks 1 & 2 (`&mcp.StdioTransport{}`). ✓
- Separate nested modules + go.work → Tasks 1, 2, 3. ✓
- echo & ping, one tool per module → Tasks 1, 2. ✓
- Pure handler tested without transport → `runEcho`/`runPing` + `_test.go` in Tasks 1, 2. ✓
- Commit go.work, ignore go.work.sum, ignore /bin/ → Task 4. ✓
- Makefile `mcps` + `test-mcps` → Task 5. ✓
- stdout-is-protocol / stderr-for-logs note → comment in both `main.go` (Tasks 1, 2) + smoke test (Task 5). ✓
- Out of scope (registration, extra tools, shared module) → not implemented, as intended. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command shows expected output. ✓

**Type consistency:** `EchoInput.Text`/`EchoOutput.Text`/`runEcho` used identically in `echo.go`, `echo_test.go`, `main.go`. `PingOutput.Time`/`PingOutput.Message`/`runPing` used identically in `ping.go`, `ping_test.go`, `main.go`. Module paths `github.com/benenen/myclaw/mcps/{echo,ping}` match `go.work` and the Makefile `./mcps/{echo,ping}` build paths. ✓
