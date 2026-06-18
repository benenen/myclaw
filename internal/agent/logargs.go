package agent

import "fmt"

// SummarizeArgs returns a copy of args with long values truncated, so launch
// logs don't dump huge flags like --append-system-prompt or --mcp-config.
func SummarizeArgs(args []string) []string {
	const maxRunes = 64
	out := make([]string, len(args))
	for i, a := range args {
		r := []rune(a)
		if len(r) > maxRunes {
			out[i] = string(r[:48]) + fmt.Sprintf("…(%d runes)", len(r))
		} else {
			out[i] = a
		}
	}
	return out
}
