package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxTraceLines = 25

// traceStep is one rendered tool line.
type traceStep struct {
	tool   string
	target string
}

// traceState is the immutable snapshot buildProgressCard renders.
type traceState struct {
	steps    []traceStep
	terminal string // "" in-progress | "done" | "fail"
	reason   string // failure reason when terminal == "fail"
	elapsed  time.Duration
}

var toolEmojis = map[string]string{
	"Bash":      "🔧",
	"Read":      "📖",
	"Edit":      "✏️",
	"Write":     "✏️",
	"Grep":      "🔍",
	"Glob":      "🔍",
	"WebFetch":  "🌐",
	"WebSearch": "🌐",
	"Task":      "🤖",
}

func toolEmoji(tool string) string {
	if e, ok := toolEmojis[tool]; ok {
		return e
	}
	return "▸"
}

func traceHeader(st traceState) string {
	switch st.terminal {
	case "done":
		return fmt.Sprintf("✅ 完成 · %d 步 · %ds", len(st.steps), int(st.elapsed.Seconds()))
	case "fail":
		if strings.TrimSpace(st.reason) != "" {
			return "⚠️ 失败：" + st.reason
		}
		return "⚠️ 失败"
	default:
		return "🤖 处理中…"
	}
}

// buildProgressCard renders the trace as a feishu interactive card. Only the
// last maxTraceLines steps are shown; overflow is summarized at the top.
func buildProgressCard(st traceState) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(traceHeader(st))
	b.WriteString("**")

	steps := st.steps
	if len(steps) > maxTraceLines {
		fmt.Fprintf(&b, "\n…(+%d 步)", len(steps)-maxTraceLines)
		steps = steps[len(steps)-maxTraceLines:]
	}
	for _, s := range steps {
		b.WriteString("\n")
		b.WriteString(toolEmoji(s.tool))
		b.WriteString(" ")
		b.WriteString(s.tool)
		if s.target != "" {
			b.WriteString("  ")
			b.WriteString(s.target)
		}
	}

	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"elements": []any{map[string]any{"tag": "markdown", "content": b.String()}},
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(encoded)
}
