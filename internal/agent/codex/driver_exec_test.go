package codex

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

func TestExecDriverRegistersOneshot(t *testing.T) {
	driver, ok := agent.LookupDriver(execDriverName)
	if !ok {
		t.Fatal("expected codex-exec driver registration")
	}
	if _, ok := driver.(*ExecDriver); !ok {
		t.Fatalf("driver type = %T, want *ExecDriver", driver)
	}
}

func TestExecDriverInitRejectsEmptyCommand(t *testing.T) {
	driver := NewExecDriver()
	_, err := driver.Init(context.Background(), agent.Spec{Type: execDriverName})
	if err == nil {
		t.Fatal("expected empty command error")
	}
}

func TestLastCompletedItemTextReturnsLastCompletedText(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"thread.started","thread_id":"th_1"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","aggregated_output":"x"}}`,
		`{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"second"}}`,
	}, "\n")

	got, err := lastCompletedItemText(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Fatalf("lastCompletedItemText() = %q", got)
	}
}

func TestLastCompletedItemTextReturnsErrorWhenMissing(t *testing.T) {
	_, err := lastCompletedItemText(`{"type":"turn.completed"}`)
	if err == nil || !strings.Contains(err.Error(), "missing completed item text") {
		t.Fatalf("lastCompletedItemText() error = %v", err)
	}
}

func TestExecRuntimeRunReturnsLastCompletedItemText(t *testing.T) {
	runtime := &ExecRuntime{spec: agent.Spec{
		Type:    execDriverName,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexExecDriver", "--", "exec-success"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: time.Second,
	}}

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "你好"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "你好，有什么要我处理的？" {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	if !strings.Contains(resp.RawOutput, `"type":"item.completed"`) {
		t.Fatalf("Run() raw output = %q", resp.RawOutput)
	}
}

func TestHelperProcessCodexExecDriver(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	n := len(args)
	if n < 6 || args[n-5] != "exec-success" {
		os.Exit(2)
	}
	if args[n-4] != "exec" || args[n-3] != "--json" || args[n-2] != "--skip-git-repo-check" || args[n-1] != "你好" {
		fmt.Fprintf(os.Stderr, "unexpected args: %#v", args)
		os.Exit(3)
	}

	lines := []string{
		`{"type":"thread.started","thread_id":"019d8c9c-e35a-7712-a73a-6fa85695495a"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"我先按会话要求检查要用的技能流程，然后直接接上你的问题。"}}`,
		`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"sed -n '1,200p' /Users/tangchuanyu/.codex/superpowers/skills/using-superpowers/SKILL.md","aggregated_output":"","exit_code":null,"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"sed -n '1,200p' /Users/tangchuanyu/.codex/superpowers/skills/using-superpowers/SKILL.md","aggregated_output":"...","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"使用 \u0060using-superpowers\u0060，先确认本轮需要遵循的技能流程。"}}`,
		`{"type":"item.completed","item":{"id":"item_3","type":"agent_message","text":"你好，有什么要我处理的？"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":28011,"cached_input_tokens":15744,"output_tokens":349}}`,
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	os.Exit(0)
}
