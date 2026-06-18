package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummarizeArgs(t *testing.T) {
	t.Run("short arg passes through unchanged", func(t *testing.T) {
		args := []string{"--verbose", "hello"}
		got := SummarizeArgs(args)
		if len(got) != len(args) {
			t.Fatalf("expected %d args, got %d", len(args), len(got))
		}
		for i, v := range args {
			if got[i] != v {
				t.Errorf("arg[%d]: expected %q, got %q", i, v, got[i])
			}
		}
	})

	t.Run("arg longer than 64 runes is truncated and contains 'runes)'", func(t *testing.T) {
		long := strings.Repeat("x", 65)
		got := SummarizeArgs([]string{long})
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if got[0] == long {
			t.Error("expected truncation but got original string back")
		}
		if !strings.Contains(got[0], "runes)") {
			t.Errorf("expected truncated string to contain 'runes)', got: %q", got[0])
		}
		// Must be shorter than the original
		if len([]rune(got[0])) >= 65 {
			t.Errorf("expected truncated result to be shorter than 65 runes, got %d", len([]rune(got[0])))
		}
	})

	t.Run("multibyte Chinese long arg is valid UTF-8 and does not panic", func(t *testing.T) {
		// Each Chinese character is multiple bytes but one rune.
		// Build a string of 70 Chinese characters (> 64 runes).
		long := strings.Repeat("你好世界啊", 14) // 5 runes * 14 = 70 runes
		got := SummarizeArgs([]string{long})
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if !utf8.ValidString(got[0]) {
			t.Errorf("result is not valid UTF-8: %q", got[0])
		}
		if !strings.Contains(got[0], "runes)") {
			t.Errorf("expected truncated string to contain 'runes)', got: %q", got[0])
		}
	})
}
