package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

func TestACPDriverRegistersOpencodeACP(t *testing.T) {
	driver, ok := agent.LookupDriver(acpDriverName)
	if !ok {
		t.Fatal("expected opencode-acp driver registration")
	}
	if _, ok := driver.(*ACPDriver); !ok {
		t.Fatalf("driver type = %T, want *ACPDriver", driver)
	}
}

func TestACPDriverInitRejectsEmptyCommand(t *testing.T) {
	driver := NewACPDriver()
	_, err := driver.Init(context.Background(), agent.Spec{Type: acpDriverName})
	if err == nil {
		t.Fatal("expected empty command error")
	}
}

func TestACPRuntimeRunUsesOpencodeACPProtocol(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessOpencodeACP", "--", "acp-success"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         logPath,
		},
		WorkDir: workDir,
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})

	first, err := runtime.Run(context.Background(), agent.Request{Prompt: "hello", WorkDir: workDir})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.Text != "reply:hello" {
		t.Fatalf("first Run() text = %q", first.Text)
	}
	if first.RuntimeType != runtimeTypeOpencode {
		t.Fatalf("first Run() runtime type = %q", first.RuntimeType)
	}

	second, err := runtime.Run(context.Background(), agent.Request{Prompt: "world", WorkDir: workDir})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.Text != "reply:world" {
		t.Fatalf("second Run() text = %q", second.Text)
	}
	if second.RuntimeType != runtimeTypeOpencode {
		t.Fatalf("second Run() runtime type = %q", second.RuntimeType)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if countACPMethod(lines, "initialize") != 1 {
		t.Fatalf("initialize count = %d, want 1", countACPMethod(lines, "initialize"))
	}
	if countACPMethod(lines, "initialized") != 1 {
		t.Fatalf("initialized count = %d, want 1", countACPMethod(lines, "initialized"))
	}
	if countACPMethod(lines, "session/new") != 1 {
		t.Fatalf("session/new count = %d, want 1", countACPMethod(lines, "session/new"))
	}
	if countACPMethod(lines, "session/prompt") != 2 {
		t.Fatalf("session/prompt count = %d, want 2", countACPMethod(lines, "session/prompt"))
	}
	if !strings.Contains(string(logData), `"cwd":"`+workDir+`"`) {
		t.Fatalf("log missing cwd %q: %s", workDir, string(logData))
	}
	if !strings.Contains(string(logData), `"text":"hello"`) || !strings.Contains(string(logData), `"text":"world"`) {
		t.Fatalf("log missing prompt texts: %s", string(logData))
	}

	// The session id created by session/new (session-1) must be surfaced on
	// every completed turn so the orchestrator can persist it for later reuse.
	if first.SessionID != "session-1" {
		t.Fatalf("first Run() SessionID = %q, want session-1", first.SessionID)
	}
	if second.SessionID != "session-1" {
		t.Fatalf("second Run() SessionID = %q, want session-1", second.SessionID)
	}
}

func TestACPRuntimeReusesResumedSession(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-reuse.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessOpencodeACP", "--", "acp-success"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         logPath,
		},
		WorkDir:         workDir,
		Timeout:         2 * time.Second,
		ResumeSessionID: "sess_prior",
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "again", WorkDir: workDir})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "reply:again" {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	// The adopted resume id is used directly; no session/new round trip.
	if resp.SessionID != "sess_prior" {
		t.Fatalf("Run() SessionID = %q, want sess_prior", resp.SessionID)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if got := countACPMethod(lines, "session/new"); got != 0 {
		t.Fatalf("session/new count = %d, want 0 (adopted resume should skip new)", got)
	}
	if !strings.Contains(string(logData), `"sessionId":"sess_prior"`) {
		t.Fatalf("log missing reused session id: %s", string(logData))
	}
}

func TestACPRuntimeRecoversWhenReusedSessionRejected(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-reuse-reject.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessOpencodeACP", "--", "acp-reject-reused"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         logPath,
		},
		WorkDir:         workDir,
		Timeout:         2 * time.Second,
		ResumeSessionID: "sess_gone",
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "recover", WorkDir: workDir})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "reply:recover" {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	// The reused id was rejected; the driver recovered via session/new and must
	// surface the freshly created id.
	if resp.SessionID != "session-1" {
		t.Fatalf("Run() SessionID = %q, want session-1 (recovered session)", resp.SessionID)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if got := countACPMethod(lines, "session/new"); got != 1 {
		t.Fatalf("session/new count = %d, want 1 (recover after rejected reuse)", got)
	}
	if got := countACPMethod(lines, "session/prompt"); got != 2 {
		t.Fatalf("session/prompt count = %d, want 2 (initial reject + retry)", got)
	}
}

// TestACPDriverEmitsToolProgress asserts that a session/update carrying a
// tool_call_update (in_progress) notification causes exactly one ProgressEvent
// to be delivered via Request.OnProgress, and that the existing text/contentItem
// path still works correctly alongside it.
func TestACPDriverEmitsToolProgress(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-tool-call.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessOpencodeACP", "--", "acp-tool-call"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         logPath,
		},
		WorkDir: workDir,
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	var mu sync.Mutex
	var events []agent.ProgressEvent

	resp, err := runtime.Run(context.Background(), agent.Request{
		Prompt:  "hello",
		WorkDir: workDir,
		OnProgress: func(ev agent.ProgressEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "reply:hello" {
		t.Fatalf("Run() text = %q, want reply:hello", resp.Text)
	}

	mu.Lock()
	defer mu.Unlock()

	// The fake emits two tool_call_update(in_progress) for the same toolCallId;
	// the driver must deduplicate to exactly one ProgressEvent.
	if len(events) != 1 {
		t.Fatalf("got %d ProgressEvents, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Kind != "tool" {
		t.Errorf("ProgressEvent.Kind = %q, want tool", ev.Kind)
	}
	// title="bash" from the fake's rawInput; TargetFromInput("bash","ls -la") → "ls -la"
	if ev.Tool != "bash" {
		t.Errorf("ProgressEvent.Tool = %q, want bash", ev.Tool)
	}
	if ev.Target != "ls -la" {
		t.Errorf("ProgressEvent.Target = %q, want ls -la", ev.Target)
	}
}

func countACPMethod(lines []string, method string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, `"method":"`+method+`"`) {
			count++
		}
	}
	return count
}

func TestHelperProcessOpencodeACP(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	mode := ""
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--" {
			mode = args[i+1]
		}
	}
	if mode != "acp-success" && mode != "acp-reject-reused" && mode != "acp-tool-call" {
		os.Exit(2)
	}

	logPath := os.Getenv("MYCLAW_ACP_LOG")
	if logPath == "" {
		fmt.Fprintln(os.Stderr, "missing MYCLAW_ACP_LOG")
		os.Exit(3)
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	sessionID := ""
	sessionCount := 0
	promptCount := 0
	currentPrompt := ""
	// known tracks session ids the fake itself created via session/new. In
	// acp-reject-reused mode a prompt for an unknown (adopted) id is rejected so
	// the driver must recover via session/new and retry.
	known := map[string]bool{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		appendACPLog(logPath, line)

		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			fmt.Fprintf(os.Stderr, "decode message: %v", err)
			os.Exit(4)
		}

		method, _ := msg["method"].(string)
		id, hasID := msg["id"]

		switch method {
		case "initialize":
			params, _ := msg["params"].(map[string]any)
			if _, ok := params["protocolVersion"].(float64); !ok {
				// Real opencode 1.15.13 requires protocolVersion (a number).
				writeACPJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error":   map[string]any{"code": -32602, "message": "Invalid params: protocolVersion required"},
				})
				continue
			}
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentInfo":       map[string]any{"name": "opencode-acp", "version": "test"},
				},
			})
		case "initialized":
			if hasID {
				fmt.Fprintln(os.Stderr, "initialized should be notification")
				os.Exit(5)
			}
		case "session/new":
			params, _ := msg["params"].(map[string]any)
			if _, ok := params["mcpServers"].([]any); !ok {
				// Real opencode 1.15.13 requires mcpServers as an array.
				writeACPJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error":   map[string]any{"code": -32602, "message": "Invalid params: mcpServers must be an array"},
				})
				continue
			}
			sessionCount++
			sessionID = fmt.Sprintf("session-%d", sessionCount)
			known[sessionID] = true
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"sessionId": sessionID,
				},
			})
		case "session/prompt":
			promptCount++
			params, _ := msg["params"].(map[string]any)
			gotSessionID, _ := params["sessionId"].(string)
			// Real opencode 1.15.13 requires prompt as an array of content blocks
			// ([{"type":"text","text":"..."}]); a bare "text" string is rejected.
			promptArr, ok := params["prompt"].([]any)
			if !ok {
				writeACPJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error":   map[string]any{"code": -32602, "message": "Invalid params: prompt must be an array"},
				})
				continue
			}
			currentPrompt = ""
			if len(promptArr) > 0 {
				if block, ok := promptArr[0].(map[string]any); ok {
					currentPrompt, _ = block["text"].(string)
				}
			}
			if mode == "acp-reject-reused" && !known[gotSessionID] {
				// Adopted resume id the server does not recognize.
				writeACPJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error":   map[string]any{"code": -32602, "message": "unknown session id"},
				})
				continue
			}
			// In acp-success / acp-tool-call mode an adopted resume id is
			// accepted as-is.
			sessionID = gotSessionID

			// In acp-tool-call mode emit two tool_call_update(in_progress) for the
			// same call before the text — the driver must deduplicate to one event.
			if mode == "acp-tool-call" {
				for i := 0; i < 2; i++ {
					writeACPJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": sessionID,
							"update": map[string]any{
								"sessionUpdate": "tool_call_update",
								"toolCallId":    "call_test_001",
								"status":        "in_progress",
								"title":         "bash",
								"kind":          "execute",
								"rawInput":      map[string]any{"command": "ls -la"},
							},
						},
					})
				}
			}

			// Real opencode streams the answer as agent_message_chunk updates.
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sessionID,
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"messageId":     fmt.Sprintf("msg-%d", promptCount),
						"content":       map[string]any{"type": "text", "text": "reply:" + currentPrompt},
					},
				},
			})

			// The session/prompt RPC result (with stopReason) is the turn-completion
			// signal — there is no contentItem "completed" event.
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"stopReason": "end_turn"},
			})
		default:
			if result, ok := msg["result"].(map[string]any); ok {
				outcome, _ := result["outcome"].(map[string]any)
				if optionID, _ := outcome["optionId"].(string); optionID == "allow" {
					appendACPLog(logPath, `{"method":"allow-response"}`)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scan stdin: %v", err)
		os.Exit(8)
	}
	os.Exit(0)
}

func appendACPLog(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open log: %v", err)
		os.Exit(9)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, line); err != nil {
		fmt.Fprintf(os.Stderr, "write log: %v", err)
		os.Exit(10)
	}
}

func writeACPJSON(v map[string]any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal response: %v", err)
		os.Exit(11)
	}
	fmt.Println(string(data))
}

func TestBuildACPArgsInjectsForAliasedRealCLI(t *testing.T) {
	got := buildACPArgs("oc", nil, true)
	if len(got) == 0 || got[0] != "acp" {
		t.Fatalf("expected acp injected for realCLI alias, got %v", got)
	}
}

func TestBuildACPArgsSkipsForStubWhenNotRealCLI(t *testing.T) {
	got := buildACPArgs("/tmp/fake-opencode", []string{"x"}, false)
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("expected verbatim args for stub, got %v", got)
	}
}
