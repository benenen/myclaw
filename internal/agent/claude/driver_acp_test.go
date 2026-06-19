package claude

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

func TestACPDriverRegistersClaudeACP(t *testing.T) {
	driver, ok := agent.LookupDriver(acpDriverName)
	if !ok {
		t.Fatal("expected claude-acp driver registration")
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

func TestACPRuntimeRunStreamsMultiTurn(t *testing.T) {
	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "acp.log")

	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessClaudeACP", "--", "claude-stream"},
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
	if first.RuntimeType != runtimeTypeClaude {
		t.Fatalf("first Run() runtime type = %q", first.RuntimeType)
	}

	second, err := runtime.Run(context.Background(), agent.Request{Prompt: "second", WorkDir: workDir})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.Text != "reply:second" {
		t.Fatalf("second Run() text = %q", second.Text)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	// One persistent process must have served both turns.
	if got := strings.Count(string(logData), `"type":"user"`); got != 2 {
		t.Fatalf("user message count = %d, want 2: %s", got, string(logData))
	}
	if !strings.Contains(string(logData), `"text":"first"`) || !strings.Contains(string(logData), `"text":"second"`) {
		t.Fatalf("log missing prompt texts: %s", string(logData))
	}
}

func TestACPRuntimeRunReturnsTurnError(t *testing.T) {
	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessClaudeACP", "--", "claude-stream"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         filepath.Join(t.TempDir(), "acp.log"),
		},
		WorkDir: t.TempDir(),
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "boom"})
	if err == nil {
		t.Fatal("expected turn error for boom prompt")
	}
	if resp.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", resp.ExitCode)
	}
	if !strings.Contains(resp.Text, "kaboom") {
		t.Fatalf("error text = %q, want it to contain kaboom", resp.Text)
	}
}

func TestHelperProcessClaudeACP(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := ""
	args := os.Args
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--" {
			mode = args[i+1]
		}
	}
	if mode != "claude-stream" {
		os.Exit(2)
	}

	logPath := os.Getenv("MYCLAW_ACP_LOG")

	// Announce readiness exactly like Claude Code's stream-json init event.
	writeStreamJSON(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "test-session",
	})

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if logPath != "" {
			appendACPLog(logPath, line)
		}

		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.Type != "user" {
			continue
		}
		prompt := ""
		if len(msg.Message.Content) > 0 {
			prompt = msg.Message.Content[0].Text
		}

		if prompt == "boom" {
			writeStreamJSON(map[string]any{
				"type":     "result",
				"subtype":  "error_during_execution",
				"is_error": true,
				"result":   "kaboom",
			})
			continue
		}

		// Stream an assistant delta (ignored by the driver) followed by the
		// authoritative result event that closes the turn.
		writeStreamJSON(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "reply:" + prompt}},
			},
		})
		writeStreamJSON(map[string]any{
			"type":     "result",
			"subtype":  "success",
			"is_error": false,
			"result":   "reply:" + prompt,
		})
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

func writeStreamJSON(v map[string]any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v", err)
		os.Exit(11)
	}
	fmt.Println(string(data))
}

func TestBuildACPArgsInjectsForAliasedRealCLI(t *testing.T) {
	got := buildACPArgs("cl", nil, true, "")
	if len(got) == 0 || got[0] != "-p" {
		t.Fatalf("expected -p injected for realCLI alias, got %v", got)
	}
}

func TestBuildACPArgsSkipsForStubWhenNotRealCLI(t *testing.T) {
	got := buildACPArgs("/tmp/fake-claude", []string{"x"}, false, "")
	if len(got) != 1 || got[0] != "x" {
		t.Fatalf("expected verbatim args for stub, got %v", got)
	}
}

func TestBuildACPArgsAddsResumeWhenSet(t *testing.T) {
	got := buildACPArgs("claude", nil, false, "sess_abc")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--resume sess_abc") {
		t.Fatalf("expected --resume sess_abc, got %v", got)
	}
}

func TestBuildACPArgsNoResumeWhenEmpty(t *testing.T) {
	got := buildACPArgs("claude", nil, false, "")
	if strings.Contains(strings.Join(got, " "), "--resume") {
		t.Fatalf("did not expect --resume, got %v", got)
	}
}

func TestReadLoop_EmitsToolUseProgress(t *testing.T) {
	var got []agent.ProgressEvent
	var mu sync.Mutex
	r := &ACPRuntime{activeProgress: func(ev agent.ProgressEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}}

	line := `{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"working"},` +
		`{"type":"tool_use","name":"Bash","input":{"command":"boo ls"}},` +
		`{"type":"tool_use","name":"Read","input":{"file_path":"internal/api.go"}}]}}`

	r.handleLine(line)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Tool != "Bash" || got[0].Target != "boo ls" {
		t.Fatalf("event0 = %+v", got[0])
	}
	if got[1].Tool != "Read" || got[1].Target != "internal/api.go" {
		t.Fatalf("event1 = %+v", got[1])
	}
}

func TestACPRuntimeCapturesSessionID(t *testing.T) {
	driver := NewACPDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    acpDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessClaudeACP", "--", "claude-stream"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"MYCLAW_ACP_LOG":         filepath.Join(t.TempDir(), "acp.log"),
		},
		WorkDir: t.TempDir(),
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.SessionID != "test-session" {
		t.Fatalf("SessionID = %q, want test-session", resp.SessionID)
	}
}
