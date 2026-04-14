package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOneshotDriverRegistersInRegistry(t *testing.T) {
	driver, ok := LookupDriver("oneshot")
	if !ok {
		t.Fatal("expected oneshot driver registration")
	}
	if driver == nil {
		t.Fatal("expected non-nil oneshot driver")
	}
}

func TestOneshotDriverInitReturnsRuntimeThatRunsRequests(t *testing.T) {
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "cat"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.RawOutput != "hello" {
		t.Fatalf("resp.RawOutput = %q", resp.RawOutput)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("resp.ExitCode = %d", resp.ExitCode)
	}
	if resp.Duration <= 0 {
		t.Fatalf("resp.Duration = %s", resp.Duration)
	}
}

func TestOneshotRuntimeRunFailureIncludesExitCodeAndRawOutput(t *testing.T) {
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf 'out'; printf 'err' >&2; exit 7"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if resp.Text != "out" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.RawOutput != "out\nerr" {
		t.Fatalf("resp.RawOutput = %q", resp.RawOutput)
	}
	if resp.ExitCode != 7 {
		t.Fatalf("resp.ExitCode = %d", resp.ExitCode)
	}
}

func TestOneshotRuntimeRunFailureWithStderrOnlyDoesNotPrefixNewline(t *testing.T) {
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf 'err' >&2; exit 9"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if resp.Text != "" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.RawOutput != "err" {
		t.Fatalf("resp.RawOutput = %q", resp.RawOutput)
	}
	if resp.ExitCode != 9 {
		t.Fatalf("resp.ExitCode = %d", resp.ExitCode)
	}
}

func TestOneshotRuntimeRunNormalizesLineEndingsWithoutTrimmingSpaces(t *testing.T) {
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf '  hello  \r\n'; printf '  warn  \r\n' >&2"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "  hello  " {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.RawOutput != "  hello  \n  warn  " {
		t.Fatalf("resp.RawOutput = %q", resp.RawOutput)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("resp.ExitCode = %d", resp.ExitCode)
	}
}

func TestOneshotRuntimeRunPassesEnv(t *testing.T) {
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf '%s' \"$SPECIAL_VALUE\""},
		Env: map[string]string{
			"SPECIAL_VALUE": "from-spec-env",
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "from-spec-env" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.RawOutput != "from-spec-env" {
		t.Fatalf("resp.RawOutput = %q", resp.RawOutput)
	}
}

func TestOneshotRuntimeRunUsesWorkDir(t *testing.T) {
	workDir := t.TempDir()
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "pwd"},
		WorkDir: workDir,
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != filepath.Clean(workDir) {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if resp.RawOutput != filepath.Clean(workDir) {
		t.Fatalf("resp.RawOutput = %q", resp.RawOutput)
	}
}

func TestOneshotRuntimeRunTimeout(t *testing.T) {
	runtime, err := NewOneshotDriver().Init(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "sleep 2"},
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	resp, err := runtime.Run(context.Background(), Request{Prompt: "ignored"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Duration <= 0 {
		t.Fatalf("resp.Duration = %s", resp.Duration)
	}
}
