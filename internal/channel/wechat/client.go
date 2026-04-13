package wechat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/benenen/myclaw/internal/logging"
)

type Client interface {
	CreateBindingSession(ctx context.Context, bindingID string) (CreateSessionResult, error)
	GetBindingSession(ctx context.Context, providerRef string) (GetSessionResult, error)
	GetMessagesLongPoll(ctx context.Context, opts GetUpdatesOptions) (GetUpdatesResult, error)
	SendTextMessage(ctx context.Context, opts SendMessageOptions) error
}

type Message struct {
	MsgID   string
	MsgType string
	From    string
	Text    string
	Raw     []byte
	Created int64
}

type GetUpdatesOptions struct {
	BaseURL   string
	Token     string
	WechatUIN string
	Cursor    string
	Timeout   time.Duration
}

type GetUpdatesResult struct {
	Messages    []Message
	Cursor      string
	NextTimeout time.Duration
	Ret         int    `json:"ret"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

type SendMessageOptions struct {
	BaseURL   string
	Token     string
	WechatUIN string
	ToUserID  string
	Text      string
}

type getUpdatesRequest struct {
	GetUpdatesBuf string `json:"get_updates_buf"`
	BaseInfo      struct {
		ChannelVersion string `json:"channel_version"`
	} `json:"base_info"`
}

type getUpdatesResponse struct {
	Ret           int             `json:"ret"`
	ErrCode       int             `json:"errcode"`
	ErrMsg        string          `json:"errmsg"`
	Messages      []weixinMessage `json:"msgs"`
	GetUpdatesBuf string          `json:"get_updates_buf"`
	SyncBuf       string          `json:"sync_buf"`
	LongPollingMS int             `json:"longpolling_timeout_ms"`
}

type weixinMessage struct {
	MessageID    int64         `json:"message_id"`
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	CreateTimeMS int64         `json:"create_time_ms"`
	ItemList     []messageItem `json:"item_list"`
}

type messageItem struct {
	Type     int       `json:"type"`
	MsgID    string    `json:"msg_id"`
	TextItem *textItem `json:"text_item"`
}

type textItem struct {
	Text string `json:"text"`
}

type CreateSessionResult struct {
	QRCode           string               `json:"qrcode"`
	QRCodeURL        string               `json:"qrcode_url"`
	QRCodeImgContent string               `json:"qrcode_img_content"`
	QRBase64         string               `json:"qr_base64"`
	Ticket           string               `json:"ticket"`
	URL              string               `json:"url"`
	ExpiresAt        time.Time            `json:"expires_at"`
	Data             *CreateSessionResult `json:"data"`
}

func (r CreateSessionResult) normalized() CreateSessionResult {
	if r.Data == nil {
		if r.QRBase64 != "" && r.QRCodeURL == "" {
			r.QRCodeURL = r.QRBase64
		}
		return r
	}
	data := r.Data.normalized()
	if r.QRCode == "" {
		r.QRCode = data.QRCode
	}
	if r.QRCodeURL == "" {
		r.QRCodeURL = data.QRCodeURL
	}
	if r.QRCodeImgContent == "" {
		r.QRCodeImgContent = data.QRCodeImgContent
	}
	if r.QRBase64 == "" {
		r.QRBase64 = data.QRBase64
	}
	if r.Ticket == "" {
		r.Ticket = data.Ticket
	}
	if r.URL == "" {
		r.URL = data.URL
	}
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = data.ExpiresAt
	}
	if r.QRBase64 != "" && r.QRCodeURL == "" {
		r.QRCodeURL = r.QRBase64
	}
	r.Data = nil
	return r
}

func (r CreateSessionResult) providerRef() string {
	if r.QRCode != "" {
		return r.QRCode
	}
	return r.Ticket
}

func (r CreateSessionResult) qrPayload() string {
	if r.QRCode != "" {
		return r.QRCode
	}
	if r.Ticket != "" {
		return r.Ticket
	}
	if r.QRCodeURL != "" {
		return r.QRCodeURL
	}
	if r.URL != "" {
		return r.URL
	}
	return r.QRCodeImgContent
}

func (r CreateSessionResult) qrShareURL() string {
	if r.QRCodeImgContent != "" {
		return r.QRCodeImgContent
	}
	if r.QRCodeURL != "" {
		return r.QRCodeURL
	}
	return r.URL
}

func (r CreateSessionResult) normalizedExpiry() time.Time {
	if !r.ExpiresAt.IsZero() {
		return r.ExpiresAt
	}
	return time.Now().Add(5 * time.Minute)
}

type GetSessionResult struct {
	Status            string          `json:"status"`
	QRCode            string          `json:"qrcode"`
	QRCodeURL         string          `json:"qrcode_url"`
	ExpiresAt         time.Time       `json:"expires_at"`
	OpenID            string          `json:"openid"`
	Nickname          string          `json:"nickname"`
	AvatarURL         string          `json:"avatar_url"`
	BaseURL           string          `json:"baseurl"`
	BotToken          string          `json:"bot_token"`
	ILinkBotID        string          `json:"ilink_bot_id"`
	ILinkUserID       string          `json:"ilink_user_id"`
	CredentialPayload json.RawMessage `json:"credential_payload"`
	CredentialVersion int             `json:"credential_version"`
	ErrorMessage      string          `json:"error_message"`
}

func (r GetSessionResult) qrPayload() string {
	if r.QRCodeURL != "" {
		return r.QRCodeURL
	}
	return r.QRCode
}

func (r GetSessionResult) accountUID() string {
	if r.ILinkUserID != "" {
		return r.ILinkUserID
	}
	return r.OpenID
}

func (r GetSessionResult) displayName() string {
	return r.Nickname
}

func (r GetSessionResult) normalizedStatus() string {
	switch r.Status {
	case "wait", "scaned":
		return "qr_ready"
	default:
		return r.Status
	}
}

func (r GetSessionResult) normalizedCredentialPayload() json.RawMessage {
	if len(r.CredentialPayload) != 0 {
		return r.CredentialPayload
	}
	payload, _ := json.Marshal(map[string]any{
		"openid":        r.OpenID,
		"nickname":      r.Nickname,
		"avatar":        r.AvatarURL,
		"baseurl":       r.BaseURL,
		"bot_token":     r.BotToken,
		"ilink_bot_id":  r.ILinkBotID,
		"ilink_user_id": r.ILinkUserID,
	})
	return payload
}

func (r GetSessionResult) normalizedCredentialVersion() int {
	if r.CredentialVersion != 0 {
		return r.CredentialVersion
	}
	return 1
}

func (r GetSessionResult) normalizedExpiry() time.Time {
	if !r.ExpiresAt.IsZero() {
		return r.ExpiresAt
	}
	return time.Now().Add(5 * time.Minute)
}

func buildBotQRCodeURL(baseURL string) string {
	values := url.Values{}
	values.Set("bot_type", "3")
	return baseURL + "/ilink/bot/get_bot_qrcode?" + values.Encode()
}

func buildQRCodeStatusURL(baseURL, qrcode string) string {
	values := url.Values{}
	values.Set("qrcode", qrcode)
	return baseURL + "/ilink/bot/get_qrcode_status?" + values.Encode()
}

func attachAuth(req *http.Request, authToken, wechatUIN string) {
	if authToken == "" {
		return
	}
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if wechatUIN != "" {
		req.Header.Set("X-WECHAT-UIN", wechatUIN)
	}
}

func randomWechatUIN() string {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, rand.Uint32())
	return base64.StdEncoding.EncodeToString(buf)
}

func debugLog(logger *logging.Logger, msg string, args ...any) {
	if logger != nil {
		logger.Debug(msg, args...)
	}
}

func decodeJSONResponse[T any](resp *http.Response, action string) (T, error) {
	var zero T
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("read %s response: %w", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("%s: status=%d body=%q", action, resp.StatusCode, string(body))
	}
	var result T
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return zero, fmt.Errorf("decode %s response: %w; body=%q", action, err, string(body))
	}
	return result, nil
}

func contextWithDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func decodeSendMessageResponse(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read sendmsg response: %w", err)
	}

	var result struct {
		Ret     int    `json:"ret"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("sendmsg failed: status=%d body=%q", resp.StatusCode, string(body))
		}
		return fmt.Errorf("decode sendmsg response: %w; status=%d body=%q", err, resp.StatusCode, string(body))
	}
	if resp.StatusCode != http.StatusOK || result.Ret != 0 || result.ErrCode != 0 {
		return fmt.Errorf("sendmsg failed: status=%d ret=%d errcode=%d errmsg=%q body=%q", resp.StatusCode, result.Ret, result.ErrCode, result.ErrMsg, string(body))
	}
	return nil
}

func readRequestBody(action string, err error) error {
	return fmt.Errorf("%s request: %w", action, err)
}

func readResponseBody(action string, err error) error {
	return fmt.Errorf("%s request: %w", action, err)
}

func createQRCodeRequest(ctx context.Context, baseURL string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, buildBotQRCodeURL(baseURL), nil)
}

func createStatusRequest(ctx context.Context, baseURL, qrcode string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, buildQRCodeStatusURL(baseURL, qrcode), nil)
}

func doRequest(client *http.Client, req *http.Request, action string) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", action, err)
	}
	return resp, nil
}

func closeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
}

func createBindingActionName() string { return "create binding" }
func getBindingActionName() string    { return "get binding" }

func createBindingRequestPath(baseURL string) string { return buildBotQRCodeURL(baseURL) }
func getBindingRequestPath(baseURL, qrcode string) string {
	return buildQRCodeStatusURL(baseURL, qrcode)
}

func mapCreateSessionResult(result CreateSessionResult) CreateSessionResult { return result }
func mapGetSessionResult(result GetSessionResult) GetSessionResult          { return result }

func createBindingRequest(ctx context.Context, baseURL string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, createBindingRequestPath(baseURL), nil)
}

func getBindingRequest(ctx context.Context, baseURL, qrcode string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, getBindingRequestPath(baseURL, qrcode), nil)
}

func decodeCreateSession(resp *http.Response) (CreateSessionResult, error) {
	return decodeJSONResponse[CreateSessionResult](resp, createBindingActionName())
}

func decodeGetSession(resp *http.Response) (GetSessionResult, error) {
	return decodeJSONResponse[GetSessionResult](resp, getBindingActionName())
}

func normalizeCreateSession(result CreateSessionResult) CreateSessionResult {
	return mapCreateSessionResult(result.normalized())
}
func normalizeGetSession(result GetSessionResult) GetSessionResult {
	return mapGetSessionResult(result)
}

func createBindingError(err error) error { return readRequestBody(createBindingActionName(), err) }
func getBindingError(err error) error    { return readResponseBody(getBindingActionName(), err) }

func closeResponse(resp *http.Response) { closeBody(resp) }

func getQrcodeIdentifier(providerRef string) string { return providerRef }

func createBindingRequestWithAuth(ctx context.Context, baseURL, authToken string) (*http.Request, error) {
	req, err := createBindingRequest(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	attachAuth(req, authToken, "")
	return req, nil
}

func getBindingRequestWithAuth(ctx context.Context, baseURL, authToken, qrcode string) (*http.Request, error) {
	req, err := getBindingRequest(ctx, baseURL, qrcode)
	if err != nil {
		return nil, err
	}
	attachAuth(req, authToken, "")
	return req, nil
}

func performCreateBinding(client *http.Client, req *http.Request) (CreateSessionResult, error) {
	resp, err := doRequest(client, req, createBindingActionName())
	if err != nil {
		return CreateSessionResult{}, err
	}
	defer closeResponse(resp)
	result, err := decodeCreateSession(resp)
	if err != nil {
		return CreateSessionResult{}, err
	}
	return normalizeCreateSession(result), nil
}

func performGetBinding(client *http.Client, req *http.Request) (GetSessionResult, error) {
	resp, err := doRequest(client, req, getBindingActionName())
	if err != nil {
		return GetSessionResult{}, err
	}
	defer closeResponse(resp)
	result, err := decodeGetSession(resp)
	if err != nil {
		return GetSessionResult{}, err
	}
	return normalizeGetSession(result), nil
}

func sessionCredentialPayload(result GetSessionResult) json.RawMessage {
	return result.normalizedCredentialPayload()
}

func sessionCredentialVersion(result GetSessionResult) int {
	return result.normalizedCredentialVersion()
}

func sessionStatus(result GetSessionResult) string {
	return result.normalizedStatus()
}

func sessionExpiry(result GetSessionResult) time.Time {
	return result.normalizedExpiry()
}

func qrPayload(result CreateSessionResult) string {
	return result.qrPayload()
}

func createExpiry(result CreateSessionResult) time.Time {
	return result.normalizedExpiry()
}

func sessionPayload(result GetSessionResult) string {
	return result.qrPayload()
}

func sessionAccountUID(result GetSessionResult) string {
	return result.accountUID()
}

func sessionDisplayName(result GetSessionResult) string {
	return result.displayName()
}

func sessionAvatarURL(result GetSessionResult) string {
	return result.AvatarURL
}

func sessionErrorMessage(result GetSessionResult) string {
	return result.ErrorMessage
}

func sessionQRCode(providerRef string) string {
	return getQrcodeIdentifier(providerRef)
}

func authRequest(req *http.Request, authToken string) {
	attachAuth(req, authToken, "")
}

func httpActionCreateBinding() string { return createBindingActionName() }
func httpActionGetBinding() string    { return getBindingActionName() }

func createBindingHTTPPath(baseURL string) string { return createBindingRequestPath(baseURL) }
func getBindingHTTPPath(baseURL, qrcode string) string {
	return getBindingRequestPath(baseURL, qrcode)
}

func createBindingHTTPReq(ctx context.Context, baseURL string) (*http.Request, error) {
	return createBindingRequest(ctx, baseURL)
}

func getBindingHTTPReq(ctx context.Context, baseURL, qrcode string) (*http.Request, error) {
	return getBindingRequest(ctx, baseURL, qrcode)
}

func performHTTPCreateBinding(client *http.Client, req *http.Request) (CreateSessionResult, error) {
	return performCreateBinding(client, req)
}

func performHTTPGetBinding(client *http.Client, req *http.Request) (GetSessionResult, error) {
	return performGetBinding(client, req)
}

func requestQRCode(providerRef string) string { return sessionQRCode(providerRef) }

func createSessionQR(result CreateSessionResult) string         { return qrPayload(result) }
func createSessionExpires(result CreateSessionResult) time.Time { return createExpiry(result) }

func getSessionQR(result GetSessionResult) string          { return sessionPayload(result) }
func getSessionStatus(result GetSessionResult) string      { return sessionStatus(result) }
func getSessionAccountUID(result GetSessionResult) string  { return sessionAccountUID(result) }
func getSessionDisplayName(result GetSessionResult) string { return sessionDisplayName(result) }
func getSessionAvatarURL(result GetSessionResult) string   { return sessionAvatarURL(result) }
func getSessionCredentialPayload(result GetSessionResult) json.RawMessage {
	return sessionCredentialPayload(result)
}
func getSessionCredentialVersion(result GetSessionResult) int {
	return sessionCredentialVersion(result)
}
func getSessionExpires(result GetSessionResult) time.Time   { return sessionExpiry(result) }
func getSessionErrorMessage(result GetSessionResult) string { return sessionErrorMessage(result) }

func createBindingSessionAction() string { return httpActionCreateBinding() }
func getBindingSessionAction() string    { return httpActionGetBinding() }

func createBindingSessionRequest(ctx context.Context, baseURL, authToken string) (*http.Request, error) {
	return createBindingRequestWithAuth(ctx, baseURL, authToken)
}

func getBindingSessionRequest(ctx context.Context, baseURL, authToken, providerRef string) (*http.Request, error) {
	return getBindingRequestWithAuth(ctx, baseURL, authToken, requestQRCode(providerRef))
}

func executeCreateBindingSession(client *http.Client, req *http.Request) (CreateSessionResult, error) {
	return performHTTPCreateBinding(client, req)
}

func executeGetBindingSession(client *http.Client, req *http.Request) (GetSessionResult, error) {
	return performHTTPGetBinding(client, req)
}

func createSessionResultQRCode(result CreateSessionResult) string { return createSessionQR(result) }
func createSessionResultExpires(result CreateSessionResult) time.Time {
	return createSessionExpires(result)
}

func getSessionResultQRCode(result GetSessionResult) string     { return getSessionQR(result) }
func getSessionResultStatus(result GetSessionResult) string     { return getSessionStatus(result) }
func getSessionResultAccountUID(result GetSessionResult) string { return getSessionAccountUID(result) }
func getSessionResultDisplayName(result GetSessionResult) string {
	return getSessionDisplayName(result)
}
func getSessionResultAvatarURL(result GetSessionResult) string { return getSessionAvatarURL(result) }
func getSessionResultCredentialPayload(result GetSessionResult) json.RawMessage {
	return getSessionCredentialPayload(result)
}
func getSessionResultCredentialVersion(result GetSessionResult) int {
	return getSessionCredentialVersion(result)
}
func getSessionResultExpires(result GetSessionResult) time.Time { return getSessionExpires(result) }
func getSessionResultErrorMessage(result GetSessionResult) string {
	return getSessionErrorMessage(result)
}

type HTTPClient struct {
	baseURL   string
	authToken string
	client    *http.Client
	logger    *logging.Logger
}

func NewHTTPClient(cfg Config, logger *logging.Logger) *HTTPClient {
	return &HTTPClient{
		baseURL:   cfg.ReferenceBaseURL,
		authToken: cfg.AuthToken,
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger,
	}
}

func (c *HTTPClient) CreateBindingSession(ctx context.Context, _ string) (CreateSessionResult, error) {
	req, err := createBindingSessionRequest(ctx, c.baseURL, c.authToken)
	if err != nil {
		return CreateSessionResult{}, err
	}
	return executeCreateBindingSession(c.client, req)
}

func (c *HTTPClient) GetBindingSession(ctx context.Context, providerRef string) (GetSessionResult, error) {
	req, err := getBindingSessionRequest(ctx, c.baseURL, c.authToken, providerRef)
	if err != nil {
		return GetSessionResult{}, err
	}
	return executeGetBindingSession(c.client, req)
}

func (c *HTTPClient) GetMessagesLongPoll(ctx context.Context, opts GetUpdatesOptions) (GetUpdatesResult, error) {
	body := getUpdatesRequest{GetUpdatesBuf: opts.Cursor}
	body.BaseInfo.ChannelVersion = "1.0.0"
	payload, err := json.Marshal(body)
	if err != nil {
		return GetUpdatesResult{}, fmt.Errorf("marshal getupdates request: %w", err)
	}

	baseURL := c.baseURL
	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}
	token := c.authToken
	if opts.Token != "" {
		token = opts.Token
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/getupdates", bytes.NewReader(payload))
	if err != nil {
		return GetUpdatesResult{}, fmt.Errorf("create getupdates request: %w", err)
	}
	c.logger.Debug("wechat getupdates request", "url", req.URL.String(), "timeout", opts.Timeout, "cursor_len", len(opts.Cursor))
	req.Header.Set("Content-Type", "application/json")
	attachAuth(req, token, opts.WechatUIN)

	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		c.logger.Info("wechat getupdates transport error", "error", err)
		return GetUpdatesResult{}, fmt.Errorf("getupdates request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return GetUpdatesResult{}, fmt.Errorf("read getupdates response: %w", err)
	}
	c.logger.Debug("wechat getupdates response", "status", resp.StatusCode, "body", string(bodyBytes))

	var raw getUpdatesResponse
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		if resp.StatusCode != http.StatusOK {
			return GetUpdatesResult{}, fmt.Errorf("getupdates failed: status=%d body=%q", resp.StatusCode, string(bodyBytes))
		}
		return GetUpdatesResult{}, fmt.Errorf("decode getupdates response: %w; status=%d body=%q", err, resp.StatusCode, string(bodyBytes))
	}
	if resp.StatusCode != http.StatusOK || raw.Ret != 0 || raw.ErrCode != 0 {
		return GetUpdatesResult{Ret: raw.Ret, ErrCode: raw.ErrCode, ErrMsg: raw.ErrMsg}, fmt.Errorf("getupdates failed: status=%d ret=%d errcode=%d errmsg=%q body=%q", resp.StatusCode, raw.Ret, raw.ErrCode, raw.ErrMsg, string(bodyBytes))
	}

	result := GetUpdatesResult{
		Cursor:      raw.GetUpdatesBuf,
		NextTimeout: time.Duration(raw.LongPollingMS) * time.Millisecond,
		Ret:         raw.Ret,
		ErrCode:     raw.ErrCode,
		ErrMsg:      raw.ErrMsg,
	}
	if result.Cursor == "" {
		result.Cursor = raw.SyncBuf
	}
	for _, msg := range raw.Messages {
		parsed := Message{
			MsgID:   fmt.Sprintf("%d", msg.MessageID),
			From:    msg.FromUserID,
			Created: msg.CreateTimeMS,
		}
		for _, item := range msg.ItemList {
			if item.MsgID != "" {
				parsed.MsgID = item.MsgID
			}
			if item.TextItem != nil && item.TextItem.Text != "" {
				if parsed.Text != "" {
					parsed.Text += "\n"
				}
				parsed.Text += item.TextItem.Text
				parsed.MsgType = "text"
			}
		}
		rawBytes, _ := json.Marshal(msg)
		parsed.Raw = rawBytes
		if parsed.MsgID != "" && parsed.Text != "" {
			result.Messages = append(result.Messages, parsed)
		}
	}
	return result, nil
}

func (c *HTTPClient) SendTextMessage(ctx context.Context, opts SendMessageOptions) error {
	baseURL := c.baseURL
	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}
	token := c.authToken
	if opts.Token != "" {
		token = opts.Token
	}
	payload, err := json.Marshal(map[string]any{
		"to_user_id": opts.ToUserID,
		"text":       opts.Text,
	})
	if err != nil {
		return fmt.Errorf("marshal sendmsg request: %w", err)
	}
	requestCtx, cancel := contextWithDefaultTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, baseURL+"/ilink/bot/sendmsg", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create sendmsg request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	attachAuth(req, token, opts.WechatUIN)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("sendmsg request: %w", err)
	}
	defer resp.Body.Close()
	if err := decodeSendMessageResponse(resp); err != nil {
		return err
	}
	return nil
}
