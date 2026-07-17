package feishu

import (
	"encoding/json"
	"strings"
	"testing"
)

// A reply with newlines, markdown, quotes, backslashes, backticks and emoji
// (e.g. a markdown table answer) must serialize to VALID Feishu text content
// ({"text":"..."}), otherwise Feishu rejects it with code 230001
// "content is not a string in json format".
func TestBuildTextContentEscapesMultilineAndSpecialChars(t *testing.T) {
	text := "line1\nline2 with \"quotes\" and \\backslash\n| table | ✅ |\n`code`"
	content := buildTextContent(SendParams{Text: text})

	if !json.Valid([]byte(content)) {
		t.Fatalf("content is not valid JSON: %s", content)
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("unmarshal: %v; content=%s", err, content)
	}
	if got.Text != text {
		t.Fatalf("round-trip mismatch:\n got=%q\nwant=%q", got.Text, text)
	}
}

// Group replies (@mention prefix) must also be valid JSON even with a
// multi-line body, and keep the <at> tag.
func TestBuildTextContentMentionStillValidJSON(t *testing.T) {
	content := buildTextContent(SendParams{Text: "hi\nthere", Mentions: []string{"ou_sender"}})

	if !json.Valid([]byte(content)) {
		t.Fatalf("mention content not valid JSON: %s", content)
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(got.Text, `<at user_id="ou_sender"></at>`) {
		t.Fatalf("expected @mention prefix, got %q", got.Text)
	}
	if !strings.Contains(got.Text, "hi\nthere") {
		t.Fatalf("expected body preserved, got %q", got.Text)
	}
}

func TestIsRichMarkdown(t *testing.T) {
	rich := map[string]string{
		"table":               "header\n| a | b |\n|---|---|\n| 1 | 2 |",
		"code fence":          "see this:\n```go\nfmt.Println(\"hi\")\n```",
		"heading":             "# Title\nbody",
		"heading indented":    "   ### Sub\ntext",
		"table no outer pipe": "a | b\n--- | ---\n1 | 2",
		// Inline markdown that renders as literal noise in feishu plain text.
		"bold":         "this is **bold** text",
		"bullet list":  "- one\n- two\n- three",
		"star bullet":  "* one\n* two",
		"ordered list": "1. first\n2. second",
		"link":         "see [docs](https://example.com)",
		"cpu report":   "🖥️ **服务器资源**（2026-07-17 03:03:16）\n**CPU 使用率 13%** ｜ **内存 60%**\n\n- ✅ 新闻推送：已停\n- ✅ 每分钟 CPU/内存汇报：在跑",
		// "---" under a text line is a setext H2 heading in CommonMark, not an
		// HR — goldmark correctly treats it as a heading (the line regex could not).
		"setext heading": "above\n---\nbelow",
	}
	for name, text := range rich {
		if !isRichMarkdown(text) {
			t.Errorf("%s: expected rich, got false", name)
		}
	}
	plain := map[string]string{
		"prose":           "hello there, how are you",
		"ack":             "收到，正在处理…",
		"multiline prose": "line one\nline two\nline three",
		"midline hash":    "see issue #5 for details",
		"midline dash":    "wait - then continue",
		"bare url":        "see https://example.com for details",
		"inline star":     "2 * 3 * 4 = 24",
		"empty":           "",
	}
	for name, text := range plain {
		if isRichMarkdown(text) {
			t.Errorf("%s: expected plain, got rich", name)
		}
	}
}

func TestBuildCardContentValidJSONWithMarkdown(t *testing.T) {
	text := "# T\n| a | b |\n|---|---|\n| 1 | 2 |"
	content := buildCardContent(SendParams{Text: text})
	if !json.Valid([]byte(content)) {
		t.Fatalf("card content not valid JSON: %s", content)
	}
	var card struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(content), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.Elements) != 1 || card.Elements[0].Tag != "markdown" {
		t.Fatalf("expected one markdown element, got %#v", card.Elements)
	}
	if card.Elements[0].Content != text {
		t.Fatalf("content mismatch: %q vs %q", card.Elements[0].Content, text)
	}
}

func TestBuildCardContentGroupMention(t *testing.T) {
	content := buildCardContent(SendParams{Text: "# hi", Mentions: []string{"ou_sender"}})
	var card struct {
		Elements []struct {
			Content string `json:"content"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(content), &card); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(card.Elements[0].Content, `<at id="ou_sender"></at>`) {
		t.Fatalf("expected card @at prefix, got %q", card.Elements[0].Content)
	}
}
