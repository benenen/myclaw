package agent

import "sort"

// ProgressEvent is one piece of intermediate execution surfaced to a channel
// while a turn runs. v1 only emits Kind "tool".
type ProgressEvent struct {
	Kind   string // "tool" (v1); reserved for "thinking" etc.
	Tool   string // canonical tool name, e.g. "Bash", "Read", "WebFetch"
	Target string // already truncated salient target (command / path / url)
}

const maxTargetLen = 60

// targetKeys maps a tool name to the input field that best describes what it
// is acting on. Unlisted tools fall back to the first string field.
var targetKeys = map[string]string{
	"Bash":      "command",
	"Read":      "file_path",
	"Edit":      "file_path",
	"Write":     "file_path",
	"WebFetch":  "url",
	"WebSearch": "query",
	"Grep":      "pattern",
	"Glob":      "pattern",
	"Task":      "description",
}

// TargetFromInput picks the salient target string from a tool's input map and
// truncates it to maxTargetLen runes. Returns "" when no string field exists.
func TargetFromInput(tool string, input map[string]any) string {
	if key, ok := targetKeys[tool]; ok {
		if v, ok := input[key].(string); ok && v != "" {
			return truncate(v, maxTargetLen)
		}
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := input[k].(string); ok && v != "" {
			return truncate(v, maxTargetLen)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
