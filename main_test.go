package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benenen/myclaw/internal/store"
)

func TestRunDefaultsToServerCommand(t *testing.T) {
	called := 0

	exitCode := runWithServer(nil, io.Discard, io.Discard, func(io.Writer) int {
		called++
		return 0
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if called != 1 {
		t.Fatalf("called = %d, want 1", called)
	}
}

func TestRunExplicitServerCommand(t *testing.T) {
	called := 0

	exitCode := runWithServer([]string{"server"}, io.Discard, io.Discard, func(io.Writer) int {
		called++
		return 0
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if called != 1 {
		t.Fatalf("called = %d, want 1", called)
	}
}

func TestRunHelpAliasesWriteUsage(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		var stdout bytes.Buffer

		exitCode := runWithServer(args, &stdout, io.Discard, func(io.Writer) int {
			t.Fatal("server should not run for help")
			return 1
		})

		if exitCode != 0 {
			t.Fatalf("args %v exitCode = %d, want 0", args, exitCode)
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Fatalf("stdout = %q, want usage text", stdout.String())
		}
	}
}

func TestRunUnknownCommandReturnsError(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := runWithServer([]string{"nope"}, io.Discard, &stderr, func(io.Writer) int {
		t.Fatal("server should not run for unknown command")
		return 1
	})

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "unknown command: nope") {
		t.Fatalf("stderr = %q, want unknown command error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestRunNotifyMarksRunDone(t *testing.T) {
	tempDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	sqlitePath := filepath.Join(tempDir, "myclaw.db")
	t.Setenv("CHANNEL_DATA_DIR", tempDir)
	t.Setenv("CHANNEL_SQLITE_PATH", sqlitePath)

	if err := os.WriteFile(filepath.Join(tempDir, ".myclaw-run-id"), []byte("run_1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithServer([]string{"notify", "codex", "helper-bot"}, &stdout, &stderr, func(io.Writer) int {
		t.Fatal("server should not run for notify")
		return 1
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "notify start: runtime=codex bot=helper-bot run_id=run_1") {
		t.Fatalf("stderr = %q, want notify start log", stderr.String())
	}
	if !strings.Contains(stderr.String(), "notify done: runtime=codex bot=helper-bot run_id=run_1") {
		t.Fatalf("stderr = %q, want notify done log", stderr.String())
	}

	db, err := store.Open(sqlitePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	row := db.Raw("SELECT run_id, bot_name, runtime_type, status FROM agent_runs WHERE run_id = ?", "run_1").Row()
	var runID, botName, runtimeType, status string
	if err := row.Scan(&runID, &botName, &runtimeType, &status); err != nil {
		t.Fatalf("scan agent_runs row: %v", err)
	}
	if runID != "run_1" || botName != "helper-bot" || runtimeType != "codex" || status != "done" {
		t.Fatalf("agent_runs row = (%q, %q, %q, %q)", runID, botName, runtimeType, status)
	}
}

func TestRunNotifyRequiresSourceAndBotName(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := runWithServer([]string{"notify"}, io.Discard, &stderr, func(io.Writer) int {
		t.Fatal("server should not run for notify")
		return 1
	})

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "accepts 2 arg(s), received 0") {
		t.Fatalf("stderr = %q, want arg validation error", stderr.String())
	}
}
