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
