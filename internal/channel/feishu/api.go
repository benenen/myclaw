package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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

func (a *apiClient) SendText(ctx context.Context, creds Credentials, p SendParams) error {
	client := a.larkClient(creds.AppID, creds.AppSecret)
	content := buildTextContent(p)

	if p.ReplyMessageID != "" {
		resp, err := client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(p.ReplyMessageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeText).
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
			MsgType(larkim.MsgTypeText).
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

var _ feishuAPI = (*apiClient)(nil)
