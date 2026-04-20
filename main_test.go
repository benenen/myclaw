package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/benenen/myclaw/internal/config"
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

func TestRunServerReturnsFailureWhenConfigLoadFails(t *testing.T) {
	originalLoadConfig := loadConfig
	loadConfig = func() (config.Config, error) {
		return config.Config{}, errors.New("boom")
	}
	defer func() {
		loadConfig = originalLoadConfig
	}()

	var stderr bytes.Buffer

	exitCode := runServer(&stderr)

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "load config: boom") {
		t.Fatalf("stderr = %q, want load config error", stderr.String())
	}
}

func TestServiceURLUsesLocalhostForWildcardAddress(t *testing.T) {
	got := serviceURL(":8080")

	if got != "http://localhost:8080" {
		t.Fatalf("serviceURL(:8080) = %q, want %q", got, "http://localhost:8080")
	}
}

func TestServiceURLPreservesExplicitHost(t *testing.T) {
	got := serviceURL("127.0.0.1:9090")

	if got != "http://127.0.0.1:9090" {
		t.Fatalf("serviceURL(127.0.0.1:9090) = %q, want %q", got, "http://127.0.0.1:9090")
	}
}
