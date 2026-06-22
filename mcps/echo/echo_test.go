package main

import "testing"

func TestRunEcho(t *testing.T) {
	got := runEcho(EchoInput{Text: "hello"})
	if got.Text != "hello" {
		t.Fatalf("runEcho() = %q, want %q", got.Text, "hello")
	}
}
