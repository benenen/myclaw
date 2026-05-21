package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
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
