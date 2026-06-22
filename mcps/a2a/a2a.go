package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const (
	kindHTTP = "http"
	kindBoo  = "boo"
)

// Source is one config entry. kind defaults to "http".
type Source struct {
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	AuthToken   string `json:"auth_token,omitempty"`
	WaitTimeout string `json:"wait_timeout,omitempty"` // boo source default for dispatched waits
}

func (s Source) kind() string {
	if s.Kind == "" {
		return kindHTTP
	}
	return s.Kind
}

// ResolvedServer is a dispatchable target after expanding sources.
type ResolvedServer struct {
	Name        string
	Description string
	Kind        string
	Endpoint    string
	AuthToken   string
	Session     string
	WaitTimeout string
}

func loadSources(path string) ([]Source, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read a2a config %q: %w", path, err)
	}
	var sources []Source
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("parse a2a config %q: %w", path, err)
	}
	return sources, nil
}

// runBoo is the single exec seam for the `boo` CLI; tests stub it.
var runBoo = func(ctx context.Context, args ...string) (stdout []byte, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "boo", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return out.Bytes(), -1, err
	}
	return out.Bytes(), 0, nil
}

type booSession struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// resolve expands sources into live servers. http passes through; a boo source
// runs `boo ls --json` and emits one server per session. boo failures are logged
// and skipped (http sources still resolve). Duplicate names are dropped (first wins).
func resolve(ctx context.Context, sources []Source) []ResolvedServer {
	var out []ResolvedServer
	seen := map[string]bool{}
	add := func(rs ResolvedServer) {
		if rs.Name == "" || seen[rs.Name] {
			return
		}
		seen[rs.Name] = true
		out = append(out, rs)
	}
	for _, s := range sources {
		if s.kind() != kindHTTP {
			continue
		}
		add(ResolvedServer{Name: s.Name, Description: s.Description, Kind: kindHTTP, Endpoint: s.Endpoint, AuthToken: s.AuthToken})
	}
	for _, s := range sources {
		if s.kind() != kindBoo {
			continue
		}
		stdout, code, err := runBoo(ctx, "ls", "--json")
		if err != nil || code != 0 {
			log.Printf("a2a: boo ls failed (skipping boo sessions): code=%d err=%v", code, err)
			continue
		}
		var sessions []booSession
		if len(bytes.TrimSpace(stdout)) > 0 {
			if err := json.Unmarshal(stdout, &sessions); err != nil {
				log.Printf("a2a: parse boo ls --json: %v", err)
				continue
			}
		}
		wt := s.WaitTimeout
		if wt == "" {
			wt = "60s"
		}
		for _, sess := range sessions {
			add(ResolvedServer{Name: sess.Name, Description: sess.Title, Kind: kindBoo, Session: sess.Name, WaitTimeout: wt})
		}
	}
	return out
}

// ---- a2a_list tool ----

type ListInput struct{}

type ServerView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Endpoint    string `json:"endpoint"`
	Kind        string `json:"kind"`
}

type ListOutput struct {
	Servers []ServerView `json:"servers" jsonschema:"the A2A servers available to dispatch subtasks to"`
}

func runList(servers []ResolvedServer) ListOutput {
	views := make([]ServerView, 0, len(servers))
	for _, s := range servers {
		views = append(views, ServerView{Name: s.Name, Description: s.Description, Endpoint: s.Endpoint, Kind: s.Kind})
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

func (c *a2aClient) send(ctx context.Context, endpoint, authToken, prompt string) (string, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
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

func runDispatch(ctx context.Context, sources []Source, c *a2aClient, in DispatchInput) (DispatchOutput, error) {
	if in.Prompt == "" {
		return DispatchOutput{}, fmt.Errorf("prompt is required")
	}
	var target *ResolvedServer
	for _, s := range resolve(ctx, sources) {
		if s.Name == in.AgentName {
			s := s
			target = &s
			break
		}
	}
	if target == nil {
		return DispatchOutput{}, fmt.Errorf("no such a2a server: %s", in.AgentName)
	}
	switch target.Kind {
	case kindBoo:
		return DispatchOutput{}, fmt.Errorf("boo dispatch not yet implemented")
	default:
		result, err := c.send(ctx, target.Endpoint, target.AuthToken, in.Prompt)
		if err != nil {
			return DispatchOutput{}, err
		}
		return DispatchOutput{Result: result}, nil
	}
}
