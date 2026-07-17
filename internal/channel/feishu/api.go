package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	gtext "github.com/yuin/goldmark/text"
)

// apiClient is the real feishuAPI. Credential validation uses plain REST
// (token + bot info); sending uses the SDK's lark.Client, cached per app_id
// so tenant-access-tokens are reused.
type apiClient struct {
	domain     string
	httpClient *http.Client

	mu      sync.Mutex
	clients map[string]*lark.Client
}

func NewAPI(cfg Config) *apiClient {
	return &apiClient{
		domain:     cfg.Domain,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		clients:    make(map[string]*lark.Client),
	}
}

func (a *apiClient) larkClient(appID, appSecret string) *lark.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.clients[appID]; ok {
		return c
	}
	c := lark.NewClient(appID, appSecret)
	a.clients[appID] = c
	return c
}

func (a *apiClient) ValidateApp(ctx context.Context, appID, appSecret string) (AppInfo, error) {
	token, err := a.tenantAccessToken(ctx, appID, appSecret)
	if err != nil {
		return AppInfo{}, err
	}
	return a.botInfo(ctx, token)
}

func (a *apiClient) tenantAccessToken(ctx context.Context, appID, appSecret string) (string, error) {
	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.domain+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("feishu auth failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return out.TenantAccessToken, nil
}

func (a *apiClient) botInfo(ctx context.Context, token string) (AppInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.domain+"/open-apis/bot/v3/info", nil)
	if err != nil {
		return AppInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return AppInfo{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			AppName string `json:"app_name"`
			OpenID  string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return AppInfo{}, err
	}
	if out.Code != 0 {
		return AppInfo{}, fmt.Errorf("feishu bot info failed: code=%d msg=%s", out.Code, out.Msg)
	}
	return AppInfo{AppName: out.Bot.AppName, BotOpenID: out.Bot.OpenID}, nil
}

// buildTextContent constructs the JSON content string for a text message.
// When p.Mentions is non-empty each open_id is prepended as an @-mention tag
// using the SDK builder so the final text is "<at user_id="id"></at> … body".
func buildTextContent(p SendParams) string {
	text := p.Text
	if len(p.Mentions) > 0 {
		var sb strings.Builder
		for _, openID := range p.Mentions {
			sb.WriteString(`<at user_id="`)
			sb.WriteString(openID)
			sb.WriteString(`"></at>`)
		}
		sb.WriteString(" ")
		sb.WriteString(p.Text)
		text = sb.String()
	}
	encoded, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return `{"text":""}`
	}
	return string(encoded)
}

// markdownParser is a CommonMark parser (+ GFM tables/strikethrough) used to
// decide whether a reply should render as a feishu markdown card. Linkify is
// deliberately NOT enabled: a bare URL renders fine as plain text and should
// not force a card. The parser is stateless and safe for concurrent reuse.
var markdownParser = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough),
).Parser()

// richMarkdownKinds are the AST node kinds whose presence means the text
// carries markdown formatting that renders as literal noise in feishu plain
// text, so the reply should be sent as an interactive markdown card instead.
var richMarkdownKinds = map[ast.NodeKind]bool{
	ast.KindHeading:         true,
	ast.KindList:            true,
	ast.KindBlockquote:      true,
	ast.KindFencedCodeBlock: true,
	ast.KindCodeBlock:       true,
	ast.KindThematicBreak:   true,
	ast.KindEmphasis:        true,
	ast.KindLink:            true,
	ast.KindAutoLink:        true,
	ast.KindImage:           true,
	ast.KindCodeSpan:        true,
	east.KindTable:          true,
	east.KindStrikethrough:  true,
}

// isRichMarkdown parses text as CommonMark and reports whether it contains any
// formatting node (heading, list, emphasis, link, code, table, …). Parsing
// respects CommonMark rules — code spans suppress inner markers, emphasis
// flanking is honored, mid-line "#"/"-" are not headings/lists — which a line
// regex cannot do.
func isRichMarkdown(text string) bool {
	doc := markdownParser.Parse(gtext.NewReader([]byte(text)))
	rich := false
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && richMarkdownKinds[n.Kind()] {
			rich = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return rich
}

// buildCardContent builds the feishu interactive-card JSON for a markdown reply:
// {"config":{"wide_screen_mode":true},"elements":[{"tag":"markdown","content":...}]}.
// The markdown element renders tables/code/headings. Group @mentions use the
// card <at id="..."></at> syntax. json.Marshal guarantees valid escaped JSON.
func buildCardContent(p SendParams) string {
	md := p.Text
	if len(p.Mentions) > 0 {
		var sb strings.Builder
		for _, openID := range p.Mentions {
			sb.WriteString(`<at id="`)
			sb.WriteString(openID)
			sb.WriteString(`"></at>`)
		}
		sb.WriteString(" ")
		sb.WriteString(p.Text)
		md = sb.String()
	}
	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"elements": []any{map[string]any{"tag": "markdown", "content": md}},
	}
	b, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(b)
}

func (a *apiClient) SendText(ctx context.Context, creds Credentials, p SendParams) error {
	client := a.larkClient(creds.AppID, creds.AppSecret)

	if isRichMarkdown(p.Text) {
		if err := a.send(ctx, client, larkim.MsgTypeInteractive, buildCardContent(p), p); err == nil {
			return nil
		} else {
			slog.Warn("feishu card send failed; falling back to text", "error", err)
		}
	}
	return a.send(ctx, client, larkim.MsgTypeText, buildTextContent(p), p)
}

// send delivers a message of the given msg type and content, threading under
// the original message when ReplyMessageID is set, otherwise creating a new one.
func (a *apiClient) send(ctx context.Context, client *lark.Client, msgType, content string, p SendParams) error {
	if p.ReplyMessageID != "" {
		resp, err := client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(p.ReplyMessageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(msgType).
				Content(content).
				Build()).
			Build())
		if err != nil {
			return err
		}
		if !resp.Success() {
			return fmt.Errorf("feishu reply failed: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return nil
	}

	resp, err := client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.CreateMessageV1ReceiveIDTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(p.ChatID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("feishu send failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// CreateCard sends an interactive-card message (threaded when ReplyMessageID is
// set) and returns the new message id for later patching.
func (a *apiClient) CreateCard(ctx context.Context, creds Credentials, p CardParams) (string, error) {
	client := a.larkClient(creds.AppID, creds.AppSecret)
	if p.ReplyMessageID != "" {
		resp, err := client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(p.ReplyMessageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeInteractive).
				Content(p.Content).
				Build()).
			Build())
		if err != nil {
			return "", err
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu reply card failed: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data == nil || resp.Data.MessageId == nil {
			return "", fmt.Errorf("feishu reply card returned no message id")
		}
		return *resp.Data.MessageId, nil
	}

	resp, err := client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.CreateMessageV1ReceiveIDTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(p.ChatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(p.Content).
			Build()).
		Build())
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu create card failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", fmt.Errorf("feishu create card returned no message id")
	}
	return *resp.Data.MessageId, nil
}

// PatchCard replaces the content of an existing interactive card in place.
func (a *apiClient) PatchCard(ctx context.Context, creds Credentials, messageID, cardJSON string) error {
	client := a.larkClient(creds.AppID, creds.AppSecret)
	resp, err := client.Im.V1.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("feishu patch card failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

var _ feishuAPI = (*apiClient)(nil)
