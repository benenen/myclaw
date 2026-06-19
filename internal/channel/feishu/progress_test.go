package feishu

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func markdownContent(t *testing.T, cardJSON string) string {
	t.Helper()
	var card struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("card is not valid json: %v", err)
	}
	if len(card.Elements) == 0 || card.Elements[0].Tag != "markdown" {
		t.Fatalf("expected a markdown element, got %s", cardJSON)
	}
	return card.Elements[0].Content
}

func TestBuildProgressCard_InProgress(t *testing.T) {
	st := traceState{
		steps: []traceStep{{tool: "Bash", target: "boo ls"}, {tool: "Read", target: "api.go"}},
	}
	md := markdownContent(t, buildProgressCard(st))
	if !strings.Contains(md, "处理中") {
		t.Fatalf("missing in-progress header: %s", md)
	}
	if !strings.Contains(md, "🔧 Bash") || !strings.Contains(md, "boo ls") {
		t.Fatalf("missing bash step: %s", md)
	}
	if !strings.Contains(md, "📖 Read") {
		t.Fatalf("missing read step: %s", md)
	}
}

func TestBuildProgressCard_Done(t *testing.T) {
	st := traceState{
		steps:    []traceStep{{tool: "Bash", target: "boo ls"}},
		terminal: "done",
		elapsed:  26 * time.Second,
	}
	md := markdownContent(t, buildProgressCard(st))
	if !strings.Contains(md, "✅") || !strings.Contains(md, "1 步") || !strings.Contains(md, "26s") {
		t.Fatalf("bad done header: %s", md)
	}
}

func TestBuildProgressCard_Fail(t *testing.T) {
	st := traceState{terminal: "fail", reason: "处理超时"}
	md := markdownContent(t, buildProgressCard(st))
	if !strings.Contains(md, "⚠️") || !strings.Contains(md, "处理超时") {
		t.Fatalf("bad fail header: %s", md)
	}
}

func TestBuildProgressCard_CapsToLast25(t *testing.T) {
	var steps []traceStep
	for i := 0; i < 30; i++ {
		steps = append(steps, traceStep{tool: "Bash", target: "cmd"})
	}
	md := markdownContent(t, buildProgressCard(traceState{steps: steps}))
	if !strings.Contains(md, "+5 步") {
		t.Fatalf("expected overflow marker for 30 steps: %s", md)
	}
	if strings.Count(md, "🔧 Bash") != 25 {
		t.Fatalf("expected 25 rendered steps, got %d", strings.Count(md, "🔧 Bash"))
	}
}

func TestToolEmoji(t *testing.T) {
	cases := map[string]string{"Bash": "🔧", "Read": "📖", "Edit": "✏️", "WebFetch": "🌐", "Mystery": "▸"}
	for tool, want := range cases {
		if got := toolEmoji(tool); got != want {
			t.Fatalf("toolEmoji(%q)=%q want %q", tool, got, want)
		}
	}
}
