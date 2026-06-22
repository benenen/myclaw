package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Server is one registry entry (the auth token stays internal).
type Server struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
	AuthToken   string `json:"auth_token,omitempty"`
}

type Registry struct {
	servers []Server
}

func (r Registry) find(name string) (Server, bool) {
	for _, s := range r.servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// loadRegistry reads a JSON array of servers from path. Empty path -> empty
// registry (no error); a missing/invalid file -> error (main treats it as empty).
func loadRegistry(path string) (Registry, error) {
	if path == "" {
		return Registry{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("read a2a config %q: %w", path, err)
	}
	var servers []Server
	if err := json.Unmarshal(data, &servers); err != nil {
		return Registry{}, fmt.Errorf("parse a2a config %q: %w", path, err)
	}
	return Registry{servers: servers}, nil
}

// ---- a2a_list tool ----

type ListInput struct{}

type ServerView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
}

type ListOutput struct {
	Servers []ServerView `json:"servers" jsonschema:"the A2A servers available to dispatch subtasks to"`
}

func runList(reg Registry) ListOutput {
	views := make([]ServerView, 0, len(reg.servers))
	for _, s := range reg.servers {
		views = append(views, ServerView{Name: s.Name, Description: s.Description, Endpoint: s.Endpoint})
	}
	return ListOutput{Servers: views}
}

// ---- A2A client ----

type a2aClient struct{ http *http.Client }

func newA2AClient(h *http.Client) *a2aClient {
	if h == nil {
		h = http.DefaultClient
	}
	return &a2aClient{http: h}
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
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

func (c *a2aClient) send(ctx context.Context, s Server, prompt string) (string, error) {
	body := a2aRequest{
		JSONRPC: "2.0",
		ID:      newID("rpc"),
		Method:  "message/send",
		Params: map[string]any{
			"message": a2aMessage{
				Kind: "message", Role: "user", MessageID: newID("msg"),
				Parts: []a2aPart{{Kind: "text", Text: prompt}},
			},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.AuthToken)
	}
	resp, err := c.http.Do(req)
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

// extractText pulls text from either a Message result or a Task result.
func extractText(raw json.RawMessage) (string, error) {
	var msg a2aMessage
	if err := json.Unmarshal(raw, &msg); err == nil && len(msg.Parts) > 0 {
		return joinText(msg.Parts), nil
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
		return joinText(task.Status.Message.Parts), nil
	}
	for _, a := range task.Artifacts {
		if len(a.Parts) > 0 {
			return joinText(a.Parts), nil
		}
	}
	return "", nil
}

func joinText(parts []a2aPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// ---- a2a_dispatch tool ----

type DispatchInput struct {
	AgentName string `json:"agent_name" jsonschema:"name of the A2A server to dispatch to (from a2a_list)"`
	Prompt    string `json:"prompt" jsonschema:"the self-contained subtask to send"`
}
type DispatchOutput struct {
	Result string `json:"result"`
}

func runDispatch(ctx context.Context, reg Registry, c *a2aClient, in DispatchInput) (DispatchOutput, error) {
	if in.Prompt == "" {
		return DispatchOutput{}, fmt.Errorf("prompt is required")
	}
	s, ok := reg.find(in.AgentName)
	if !ok {
		return DispatchOutput{}, fmt.Errorf("no such a2a server: %s", in.AgentName)
	}
	result, err := c.send(ctx, s, in.Prompt)
	if err != nil {
		return DispatchOutput{}, err
	}
	return DispatchOutput{Result: result}, nil
}
