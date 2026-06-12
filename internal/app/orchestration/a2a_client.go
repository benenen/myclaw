package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/benenen/myclaw/internal/domain"
)

type A2AClient struct {
	http *http.Client
}

func NewA2AClient(httpClient *http.Client) *A2AClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &A2AClient{http: httpClient}
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type a2aMessage struct {
	Kind      string    `json:"kind"`
	Role      string    `json:"role"`
	MessageID string    `json:"messageId"`
	Parts     []a2aPart `json:"parts"`
}

type a2aRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type a2aResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *A2AClient) Run(ctx context.Context, a domain.RegisteredAgent, prompt string) (string, error) {
	reqBody := a2aRequest{
		JSONRPC: "2.0",
		ID:      domain.NewPrefixedID("rpc"),
		Method:  "message/send",
		Params: map[string]any{
			"message": a2aMessage{
				Kind:      "message",
				Role:      "user",
				MessageID: domain.NewPrefixedID("msg"),
				Parts:     []a2aPart{{Kind: "text", Text: prompt}},
			},
		},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if a.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.AuthToken)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("a2a request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("a2a endpoint returned %d", resp.StatusCode)
	}

	var rpc a2aResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return "", fmt.Errorf("decode a2a response: %w", err)
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("a2a error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return extractText(rpc.Result)
}

// extractText pulls text parts from either a Message result or a Task result
// (status.message or artifacts), covering the common A2A response shapes.
func extractText(raw json.RawMessage) (string, error) {
	var msg a2aMessage
	if err := json.Unmarshal(raw, &msg); err == nil && len(msg.Parts) > 0 {
		return joinTextParts(msg.Parts), nil
	}
	var task struct {
		Status struct {
			Message a2aMessage `json:"message"`
		} `json:"status"`
		Artifacts []struct {
			Parts []a2aPart `json:"parts"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &task); err != nil {
		return "", fmt.Errorf("unrecognized a2a result: %w", err)
	}
	if len(task.Status.Message.Parts) > 0 {
		return joinTextParts(task.Status.Message.Parts), nil
	}
	for _, art := range task.Artifacts {
		if len(art.Parts) > 0 {
			return joinTextParts(art.Parts), nil
		}
	}
	return "", nil
}

func joinTextParts(parts []a2aPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

var _ RemoteRunner = (*A2AClient)(nil)
