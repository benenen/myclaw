package main

import (
	"testing"
	"time"
)

func TestRunPing(t *testing.T) {
	got := runPing()
	if got.Message != "pong" {
		t.Fatalf("runPing().Message = %q, want %q", got.Message, "pong")
	}
	if _, err := time.Parse(time.RFC3339, got.Time); err != nil {
		t.Fatalf("runPing().Time = %q, not RFC3339: %v", got.Time, err)
	}
}
