package agent

import "testing"

func TestTargetFromInput_PrefersToolKey(t *testing.T) {
	got := TargetFromInput("Bash", map[string]any{"command": "boo ls", "description": "list"})
	if got != "boo ls" {
		t.Fatalf("got %q, want %q", got, "boo ls")
	}
}

func TestTargetFromInput_FilePathTools(t *testing.T) {
	got := TargetFromInput("Read", map[string]any{"file_path": "internal/api.go"})
	if got != "internal/api.go" {
		t.Fatalf("got %q, want %q", got, "internal/api.go")
	}
}

func TestTargetFromInput_FallsBackToFirstStringField(t *testing.T) {
	got := TargetFromInput("Unknown", map[string]any{"zeta": "z", "alpha": "a"})
	if got != "a" { // first by sorted key
		t.Fatalf("got %q, want %q", got, "a")
	}
}

func TestTargetFromInput_TruncatesTo60(t *testing.T) {
	long := ""
	for i := 0; i < 80; i++ {
		long += "x"
	}
	got := TargetFromInput("Bash", map[string]any{"command": long})
	if len([]rune(got)) != 60 {
		t.Fatalf("len = %d, want 60", len([]rune(got)))
	}
}

func TestRequestOnProgressInvokable(t *testing.T) {
	var got ProgressEvent
	req := Request{OnProgress: func(ev ProgressEvent) { got = ev }}
	req.OnProgress(ProgressEvent{Kind: "tool", Tool: "Bash", Target: "boo ls"})
	if got.Tool != "Bash" || got.Target != "boo ls" {
		t.Fatalf("got %+v", got)
	}
}
