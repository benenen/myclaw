package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

func TestACPDriverRegistersCodexACP(t *testing.T) {
	driver, ok := agent.LookupDriver(acpDriverName)
	if !ok {
		t.Fatal("expected codex-acp driver registration")
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

func TestACPRuntimeRunUsesCodexAppServerProtocol(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexACP", "--", "acp-success"},
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

	first, err := runtime.Run(context.Background(), agent.Request{Prompt: "first", WorkDir: workDir})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.Text != "reply:first" {
		t.Fatalf("first Run() text = %q", first.Text)
	}
	if first.RuntimeType != runtimeTypeCodex {
		t.Fatalf("first Run() runtime type = %q", first.RuntimeType)
	}

	second, err := runtime.Run(context.Background(), agent.Request{Prompt: "second", WorkDir: workDir})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.Text != "reply:second" {
		t.Fatalf("second Run() text = %q", second.Text)
	}
	if second.RuntimeType != runtimeTypeCodex {
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
	if countACPMethod(lines, "thread/start") != 1 {
		t.Fatalf("thread/start count = %d, want 1", countACPMethod(lines, "thread/start"))
	}
	if countACPMethod(lines, "turn/start") != 2 {
		t.Fatalf("turn/start count = %d, want 2", countACPMethod(lines, "turn/start"))
	}
	if countACPMethod(lines, "approval-response") != 2 {
		t.Fatalf("approval-response count = %d, want 2", countACPMethod(lines, "approval-response"))
	}
	if !strings.Contains(string(logData), `"cwd":"`+workDir+`"`) {
		t.Fatalf("log missing cwd %q: %s", workDir, string(logData))
	}
	if !strings.Contains(string(logData), `"text":"first"`) || !strings.Contains(string(logData), `"text":"second"`) {
		t.Fatalf("log missing prompt texts: %s", string(logData))
	}

	// The thread id created by thread/start (thread-1) must be surfaced on every
	// completed turn so the orchestrator can persist it for later resume.
	if first.SessionID != "thread-1" {
		t.Fatalf("first Run() SessionID = %q, want thread-1", first.SessionID)
	}
	if second.SessionID != "thread-1" {
		t.Fatalf("second Run() SessionID = %q, want thread-1", second.SessionID)
	}
}

func TestACPRuntimeResumesThreadWhenResumeSessionSet(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-resume.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexACP", "--", "acp-success"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         logPath,
		},
		WorkDir:         workDir,
		Timeout:         2 * time.Second,
		ResumeSessionID: "thr_prior",
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
	// The fake echoes the resumed thread id back, so the captured session id is
	// the prior id we asked to resume.
	if resp.SessionID != "thr_prior" {
		t.Fatalf("Run() SessionID = %q, want thr_prior", resp.SessionID)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if got := countACPMethod(lines, "thread/resume"); got != 1 {
		t.Fatalf("thread/resume count = %d, want 1", got)
	}
	if got := countACPMethod(lines, "thread/start"); got != 0 {
		t.Fatalf("thread/start count = %d, want 0 (resume should skip start)", got)
	}
	if !strings.Contains(string(logData), `"threadId":"thr_prior"`) {
		t.Fatalf("log missing resumed thread id: %s", string(logData))
	}
}

func TestACPRuntimeFallsBackToStartWhenResumeRejected(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp-resume-reject.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexACP", "--", "acp-reject-resume"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         logPath,
		},
		WorkDir:         workDir,
		Timeout:         2 * time.Second,
		ResumeSessionID: "thr_gone",
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
	// Resume was rejected; the driver must start fresh and surface that new id.
	if resp.SessionID != "thread-1" {
		t.Fatalf("Run() SessionID = %q, want thread-1 (fresh start after rejected resume)", resp.SessionID)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if got := countACPMethod(lines, "thread/resume"); got != 1 {
		t.Fatalf("thread/resume count = %d, want 1", got)
	}
	if got := countACPMethod(lines, "thread/start"); got != 1 {
		t.Fatalf("thread/start count = %d, want 1 (fallback after rejected resume)", got)
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

func TestHelperProcessCodexACP(t *testing.T) {
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
	if mode != "acp-success" && mode != "acp-reject-resume" {
		os.Exit(2)
	}

	logPath := os.Getenv("MYCLAW_ACP_LOG")
	if logPath == "" {
		fmt.Fprintln(os.Stderr, "missing MYCLAW_ACP_LOG")
		os.Exit(3)
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	threadID := ""
	threadCount := 0
	turnCount := 0
	currentPrompt := ""

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
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"serverInfo": map[string]any{"name": "codex-app-server", "version": "test"},
				},
			})
		case "initialized":
			if hasID {
				fmt.Fprintln(os.Stderr, "initialized should be notification")
				os.Exit(5)
			}
		case "thread/resume":
			if mode == "acp-reject-resume" {
				// Simulate a server that no longer knows the prior thread id.
				writeACPJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error":   map[string]any{"code": -32602, "message": "unknown thread id"},
				})
				continue
			}
			params, _ := msg["params"].(map[string]any)
			threadID, _ = params["threadId"].(string)
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"thread": map[string]any{"id": threadID},
				},
			})
		case "thread/start":
			threadCount++
			threadID = fmt.Sprintf("thread-%d", threadCount)
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"thread": map[string]any{"id": threadID},
				},
			})
		case "turn/start":
			turnCount++
			params, _ := msg["params"].(map[string]any)
			gotThreadID, _ := params["threadId"].(string)
			if gotThreadID != threadID {
				fmt.Fprintf(os.Stderr, "unexpected thread id: %q want %q", gotThreadID, threadID)
				os.Exit(6)
			}
			input, _ := params["input"].([]any)
			if len(input) == 0 {
				fmt.Fprintln(os.Stderr, "missing turn input")
				os.Exit(7)
			}
			firstInput, _ := input[0].(map[string]any)
			currentPrompt, _ = firstInput["text"].(string)

			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"turn": map[string]any{"id": fmt.Sprintf("turn-%d", turnCount)},
				},
			})

			approvalID := fmt.Sprintf("approval-%d", turnCount)
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      approvalID,
				"method":  "turn/approval/request",
				"params": map[string]any{
					"toolCall": map[string]any{"tool": "shell"},
					"options": []map[string]any{
						{"optionId": "deny", "name": "Deny", "kind": "deny"},
						{"optionId": "allow", "name": "Allow", "kind": "allow"},
					},
				},
			})
		default:
			if result, ok := msg["result"].(map[string]any); ok {
				outcome, _ := result["outcome"].(map[string]any)
				if optionID, _ := outcome["optionId"].(string); optionID == "allow" {
					appendACPLog(logPath, `{"method":"approval-response"}`)
					writeACPJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "item/agentMessage/delta",
						"params": map[string]any{
							"threadId": threadID,
							"itemId":   fmt.Sprintf("item-%d", turnCount),
							"delta":    "reply:" + currentPrompt,
						},
					})
					writeACPJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "turn/completed",
						"params": map[string]any{
							"threadId": threadID,
							"status":   "completed",
						},
					})
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
	// basename "cx" is not canonical, but realCLI=true must still inject app-server.
	got := buildACPArgs("cx", nil, true)
	if len(got) == 0 || got[0] != "app-server" {
		t.Fatalf("expected app-server injected for realCLI alias, got %v", got)
	}
}

func TestBuildACPArgsSkipsForStubWhenNotRealCLI(t *testing.T) {
	got := buildACPArgs("/tmp/fake-codex", []string{"x"}, false)
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("expected verbatim args for stub, got %v", got)
	}
}
