package opencode

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
	if mode != "acp-success" {
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
					"serverInfo": map[string]any{"name": "opencode-acp", "version": "test"},
					"capabilities": map[string]any{
						"session/prompt":       true,
						"session/new":          true,
						"session/requestPermission": true,
					},
				},
			})
		case "initialized":
			if hasID {
				fmt.Fprintln(os.Stderr, "initialized should be notification")
				os.Exit(5)
			}
		case "session/new":
			sessionCount++
			sessionID = fmt.Sprintf("session-%d", sessionCount)
			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"session": map[string]any{"id": sessionID},
				},
			})
		case "session/prompt":
			promptCount++
			params, _ := msg["params"].(map[string]any)
			gotSessionID, _ := params["sessionId"].(string)
			if gotSessionID != sessionID {
				fmt.Fprintf(os.Stderr, "unexpected session id: %q want %q", gotSessionID, sessionID)
				os.Exit(6)
			}
			currentPrompt, _ = params["text"].(string)

			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"promptId": fmt.Sprintf("prompt-%d", promptCount),
				},
			})

			writeACPJSON(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sessionID,
					"contentItem": map[string]any{
						"type": "text",
						"text": "reply:" + currentPrompt,
						"completed": true,
					},
				},
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
