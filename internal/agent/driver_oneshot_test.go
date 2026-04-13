package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOneshotDriverRunSuccess(t *testing.T) {
	driver := NewOneshotDriver()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "cat"},
		Timeout: 2 * time.Second,
	}, Request{Prompt: "hello"})
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

func TestOneshotDriverRunFailureIncludesExitCodeAndRawOutput(t *testing.T) {
	driver := NewOneshotDriver()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf 'out'; printf 'err' >&2; exit 7"},
		Timeout: 2 * time.Second,
	}, Request{Prompt: "ignored"})
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

func TestOneshotDriverRunFailureWithStderrOnlyDoesNotPrefixNewline(t *testing.T) {
	driver := NewOneshotDriver()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf 'err' >&2; exit 9"},
		Timeout: 2 * time.Second,
	}, Request{Prompt: "ignored"})
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

func TestOneshotDriverRunNormalizesLineEndingsWithoutTrimmingSpaces(t *testing.T) {
	driver := NewOneshotDriver()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf '  hello  \r\n'; printf '  warn  \r\n' >&2"},
		Timeout: 2 * time.Second,
	}, Request{Prompt: "ignored"})
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

func TestOneshotDriverRunPassesEnv(t *testing.T) {
	driver := NewOneshotDriver()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "printf '%s' \"$SPECIAL_VALUE\""},
		Env: map[string]string{
			"SPECIAL_VALUE": "from-spec-env",
		},
		Timeout: 2 * time.Second,
	}, Request{Prompt: "ignored"})
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

func TestOneshotDriverRunUsesWorkDir(t *testing.T) {
	driver := NewOneshotDriver()
	workDir := t.TempDir()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "pwd"},
		WorkDir: workDir,
		Timeout: 2 * time.Second,
	}, Request{Prompt: "ignored"})
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

func TestOneshotDriverRunTimeout(t *testing.T) {
	driver := NewOneshotDriver()

	resp, err := driver.Run(context.Background(), Spec{
		Command: "sh",
		Args:    []string{"-c", "sleep 2"},
		Timeout: 100 * time.Millisecond,
	}, Request{Prompt: "ignored"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Duration <= 0 {
		t.Fatalf("resp.Duration = %s", resp.Duration)
	}
}
