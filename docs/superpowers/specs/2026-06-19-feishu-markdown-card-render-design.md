# Feishu Markdown Card Rendering Design

## Goal
When a feishu bot reply contains rich markdown that is unreadable as raw text — a **table**, a **fenced code block**, or a **heading** — send it as a feishu **interactive card** (with a `markdown` element) so it renders properly. Plain/short replies keep going out as a normal text bubble. If the card send fails, fall back to the plain-text send so the user still gets the answer.

## Scope

In scope:
- A markdown-richness detector `isRichMarkdown(text)`.
- A card-content builder producing the feishu interactive-card JSON for a `markdown` element.
- Branch `SendText` in `internal/channel/feishu/api.go`: rich → send interactive card; else → existing plain-text path. Card failure → fall back to plain text.
- Group @mention preserved in the card (card `<at id="…"></at>` syntax) and in the text path (unchanged).

Out of scope:
- Converting markdown to feishu rich-text `post` (the card `markdown` element renders markdown directly — no conversion needed).
- A card header/title (bare markdown body — the chosen "only-when-rich, no header" UX).
- Changing `SendParams`, `ReplyGateway`, or `types.go` — all logic lives in `api.go`.
- Image rendering (markdown `![]()` is left to feishu's markdown element; we do not upload images).

## Decisions (locked during brainstorming)
1. **Only-when-rich:** card only when the reply has a table, fenced code block, or heading; everything else stays plain text.
2. **Trigger = table OR fenced code block OR heading `#`** (any one).
3. **Fallback to plain text** when the card send errors or returns `!Success`.
4. **No card header**, bare `markdown` element body.
5. All logic localized to `api.go` (+ a small detector); no interface/DTO changes.

## Background (current state)
`api.go` `SendText(creds, SendParams{ChatID, Text, ReplyMessageID, Mentions})` builds `{"text":…}` via `buildTextContent` (json.Marshal-escaped — the fix for feishu error 230001) and sends `MsgType: text` via `Message.Create`/`Reply`. Feishu cards are sent the same way but with `MsgType: interactive` and `content` = card JSON. Feishu's card `markdown` element (`{"tag":"markdown","content":"…"}`) renders GitHub-flavored markdown including tables and fenced code blocks (confirmed against feishu open-platform card docs).

## Architecture

### 1. Detector — `isRichMarkdown(text string) bool`
Returns true if any of:
- **Fenced code block:** the text contains a ```` ``` ```` fence (`strings.Contains(text, "```")`).
- **Heading:** any line matches `^\s{0,3}#{1,6}\s` (ATX heading; a leading `#` followed by a space). Guards against mid-line `#` (e.g. "issue #5") by anchoring to line start.
- **Table:** an adjacent line pair where line N contains `|` and line N+1 (trimmed) is a GFM separator — composed only of `|`, `-`, `:`, spaces and containing at least one `-` and one `|` (e.g. `|---|---|`, `:--|--:`). Requiring `|` in the separator avoids treating a `---` horizontal rule as a table.

Implemented with a small line scan + one compiled `regexp` for the heading. No false-positive on plain prose, bold, lists, or links.

### 2. Card content builder — `buildCardContent(p SendParams) string`
Builds the markdown text (group @mention prefix as in the text path, but card syntax `<at id="OPENID"></at> `), then:
```go
card := map[string]any{
    "config":   map[string]any{"wide_screen_mode": true},
    "elements": []any{map[string]any{"tag": "markdown", "content": mdText}},
}
b, _ := json.Marshal(card)
return string(b)
```
`json.Marshal` escapes newlines/quotes/etc., so the card content can never be malformed JSON (same discipline as the text fix).

### 3. `SendText` branch + fallback
```
if isRichMarkdown(p.Text):
    err := sendInteractiveCard(ctx, client, p)   // MsgType interactive, content = buildCardContent(p)
    if err == nil: return nil
    log a warning; fall through to plain text
return sendPlainText(ctx, client, p)             // the existing text path (MsgType text, buildTextContent)
```
- Refactor the current `SendText` body (the Reply/Create text send) into `sendPlainText`; add a parallel `sendInteractiveCard` that uses `MsgType: larkim.MsgTypeInteractive` and `content = buildCardContent(p)`, preserving the `ReplyMessageID` → `Message.Reply` vs `Message.Create` branch.
- The card path's group @mention uses `<at id="…"></at>` (card syntax); the text path keeps `<at user_id="…"></at>` (unchanged).

## Error handling
- Card send error or `!resp.Success()` ⇒ log a warning + send plain text (the reply still arrives, degraded to raw markdown which now sends fine).
- Empty reply text ⇒ handled upstream in `ReplyGateway` (no-op), unchanged.

## Testing
- `isRichMarkdown`: table → true; fenced code block → true; ATX heading → true; plain prose → false; bold-only/list-only/link-only → false; `---` horizontal rule without `|` → false; mid-line `#` ("issue #5") → false.
- `buildCardContent`: produces VALID JSON; unmarshals to `{config, elements:[{tag:"markdown", content}]}`; the content equals the reply text (p2p); group mention → content starts with `<at id="ou_x"></at> `.
- `SendText` branch is exercised against the existing fake/seam where feasible; the actual interactive-card send is the SDK touchpoint, verified by build + the content-builder tests + manual smoke.
- Manual smoke (needs a live app): reply containing a markdown table and a code block → renders as a card; a short "ok" reply → plain text bubble.

## Risks / notes
- The card `markdown` element's exact rendered markdown subset is feishu-defined; tables + code blocks + bold + lists render. Unsupported syntax degrades to literal text inside the card (acceptable).
- The fallback guarantees no regression: a worst-case card rejection still delivers the (raw) text.
