package main

import (
	"reflect"
	"testing"
)

func TestArgsForLs(t *testing.T) {
	got, err := argsForLs(LsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ls", "--json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestArgsForNew(t *testing.T) {
	got, _ := argsForNew(NewInput{Name: "build", Command: []string{"make", "-j"}, Cwd: "/tmp"})
	want := []string{"new", "build", "-d", "--cwd", "/tmp", "--", "make", "-j"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// no name, no command -> still detached
	got2, _ := argsForNew(NewInput{})
	if want2 := []string{"new", "-d"}; !reflect.DeepEqual(got2, want2) {
		t.Fatalf("got %v want %v", got2, want2)
	}
}

func TestArgsForSend(t *testing.T) {
	got, _ := argsForSend(SendInput{Name: "build", Text: "make test", Enter: true})
	if want := []string{"send", "build", "--text", "make test", "--enter"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	keys, _ := argsForSend(SendInput{Name: "build", Keys: "C-c"})
	if want := []string{"send", "build", "--key", "C-c"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %v want %v", keys, want)
	}
	if _, err := argsForSend(SendInput{Name: "b", Text: "x", Keys: "C-c"}); err == nil {
		t.Fatal("expected error: text and keys are mutually exclusive")
	}
	if _, err := argsForSend(SendInput{Name: "b"}); err == nil {
		t.Fatal("expected error: one of text/keys required")
	}
}

func TestArgsForPeek(t *testing.T) {
	got, _ := argsForPeek(PeekInput{Name: "build", Scrollback: true})
	if want := []string{"peek", "build", "--json", "--scrollback"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestArgsForWait(t *testing.T) {
	got, _ := argsForWait(WaitInput{Name: "build", Mode: "text", Text: "PASS", Timeout: "2m"})
	if want := []string{"wait", "build", "--text", "PASS", "--timeout", "2m"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	idle, _ := argsForWait(WaitInput{Name: "build", Mode: "idle"})
	if want := []string{"wait", "build", "--idle"}; !reflect.DeepEqual(idle, want) {
		t.Fatalf("got %v want %v", idle, want)
	}
	if _, err := argsForWait(WaitInput{Name: "b", Mode: "text"}); err == nil {
		t.Fatal("expected error: text mode needs text")
	}
	if _, err := argsForWait(WaitInput{Name: "b", Mode: "bogus"}); err == nil {
		t.Fatal("expected error: bad mode")
	}
}

func TestArgsForKill(t *testing.T) {
	got, _ := argsForKill(KillInput{Name: "build"})
	if want := []string{"kill", "build"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	all, _ := argsForKill(KillInput{All: true})
	if want := []string{"kill", "--all"}; !reflect.DeepEqual(all, want) {
		t.Fatalf("got %v want %v", all, want)
	}
	if _, err := argsForKill(KillInput{Name: "b", All: true}); err == nil {
		t.Fatal("expected error: name and all mutually exclusive")
	}
	if _, err := argsForKill(KillInput{}); err == nil {
		t.Fatal("expected error: one of name/all required")
	}
}
