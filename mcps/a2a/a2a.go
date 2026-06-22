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
	"path/filepath"
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
	Name   string `json:"name"`
	Title  string `json:"title"`
	IdleMS int64  `json:"idle_ms"`
}

// booConfigDir is where boo keeps per-session restore snapshots (<session>.state).
func booConfigDir() string {
	if c := os.Getenv("BOO_CONFIG"); c != "" {
		return filepath.Dir(c)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "boo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/boo"
	}
	return filepath.Join(home, ".config", "boo")
}

// booSessionCwd reads the session's saved working directory from its snapshot.
func booSessionCwd(session string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(booConfigDir(), session+".state"))
	if err != nil {
		return "", false
	}
	cwd := strings.TrimSpace(string(data))
	if cwd == "" {
		return "", false
	}
	return cwd, true
}

// booCapabilitiesDescription reads <cwd>/boo.capabilities.json and renders a
// description (with skills appended). Returns ("", false) if absent/invalid/empty.
func booCapabilitiesDescription(cwd string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(cwd, "boo.capabilities.json"))
	if err != nil {
		return "", false
	}
	var c struct {
		Description string   `json:"description"`
		Skills      []string `json:"skills"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	desc := strings.TrimSpace(c.Description)
	if len(c.Skills) > 0 {
		if desc != "" {
			desc += " "
		}
		desc += "[skills: " + strings.Join(c.Skills, ", ") + "]"
	}
	if desc == "" {
		return "", false
	}
	return desc, true
}

// booRoster returns the live boo sessions (empty on any error).
func booRoster(ctx context.Context) []booSession {
	stdout, code, err := runBoo(ctx, "ls", "--json")
	if err != nil || code != 0 {
		log.Printf("a2a: boo ls failed (code=%d): %v", code, err)
		return []booSession{}
	}
	var sessions []booSession
	if err := json.Unmarshal(stdout, &sessions); err != nil {
		log.Printf("a2a: boo ls --json parse failed: %v", err)
		return []booSession{}
	}
	if sessions == nil {
		sessions = []booSession{}
	}
	return sessions
}

// SessionDetail is the read payload for a single boo session resource.
type SessionDetail struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	IdleMS     int64  `json:"idle_ms"`
	Cwd        string `json:"cwd"`
	Capability string `json:"capability"`
}

// enrichSession fills a session's cwd + capability from its boo snapshot.
func enrichSession(s booSession) SessionDetail {
	d := SessionDetail{Name: s.Name, Title: s.Title, IdleMS: s.IdleMS}
	if cwd, ok := booSessionCwd(s.Name); ok {
		d.Cwd = cwd
		if cap, ok := booCapabilitiesDescription(cwd); ok {
			d.Capability = cap
		}
	}
	return d
}

// booRosterDetailed returns every live session enriched with cwd + capability,
// so a single roster read carries enough for routing decisions.
func booRosterDetailed(ctx context.Context) []SessionDetail {
	out := []SessionDetail{}
	for _, s := range booRoster(ctx) {
		out = append(out, enrichSession(s))
	}
	return out
}

// booSessionDetail returns one live session's detail (false if not a live session).
func booSessionDetail(ctx context.Context, name string) (SessionDetail, bool) {
	for _, s := range booRoster(ctx) {
		if s.Name == name {
			return enrichSession(s), true
		}
	}
	return SessionDetail{}, false
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
			desc := sess.Title
			if cwd, ok := booSessionCwd(sess.Name); ok {
				if cap, ok := booCapabilitiesDescription(cwd); ok {
					desc = cap
				}
			}
			add(ResolvedServer{Name: sess.Name, Description: desc, Kind: kindBoo, Session: sess.Name, WaitTimeout: wt})
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
	case kindHTTP:
		result, err := c.send(ctx, target.Endpoint, target.AuthToken, in.Prompt)
		if err != nil {
			return DispatchOutput{}, err
		}
		return DispatchOutput{Result: result}, nil
	case kindBoo:
		result, err := dispatchBoo(ctx, target.Session, in.Prompt, target.WaitTimeout)
		if err != nil {
			return DispatchOutput{}, err
		}
		return DispatchOutput{Result: result}, nil
	default:
		return DispatchOutput{}, fmt.Errorf("unknown a2a server kind: %s", target.Kind)
	}
}

// dispatchBoo types the prompt into a boo session, waits for it to settle, and
// returns the newly-produced scrollback (best-effort: a terminal is not a clean
// request/response channel).
func dispatchBoo(ctx context.Context, session, prompt, waitTimeout string) (string, error) {
	before, err := booPeek(ctx, session)
	if err != nil {
		return "", err
	}
	// Count lines as number of '\n' characters so a trailing newline doesn't
	// produce a phantom empty element (strings.Split("a\n","\n") → ["a",""] = 2).
	beforeLines := strings.Count(before, "\n")

	if _, code, err := runBoo(ctx, "send", session, "--text", prompt, "--enter"); err != nil {
		return "", fmt.Errorf("boo not available: %w", err)
	} else if e := booDispatchErr(session, code); e != nil {
		return "", e
	}

	// wait is a settle hint; timeout (exit 4) is non-fatal.
	if _, code, err := runBoo(ctx, "wait", session, "--idle", "--timeout", waitTimeout); err != nil {
		return "", fmt.Errorf("boo not available: %w", err)
	} else if e := booDispatchErr(session, code); e != nil {
		return "", e
	}

	after, err := booPeek(ctx, session)
	if err != nil {
		return "", err
	}
	afterLines := strings.Split(after, "\n")
	if beforeLines > len(afterLines) {
		beforeLines = len(afterLines)
	}
	delta := afterLines[beforeLines:]
	return trimDelta(delta, prompt), nil
}

func booPeek(ctx context.Context, session string) (string, error) {
	out, code, err := runBoo(ctx, "peek", session, "--scrollback")
	if err != nil {
		return "", fmt.Errorf("boo not available: %w", err)
	}
	if e := booDispatchErr(session, code); e != nil {
		return "", e
	}
	return string(out), nil
}

func booDispatchErr(session string, code int) error {
	switch code {
	case 0, 4: // 4 = wait timeout, non-fatal
		return nil
	case 3:
		return fmt.Errorf("boo session not running: %s", session)
	default:
		return fmt.Errorf("boo error (exit %d) for session %s", code, session)
	}
}

// trimDelta cleans the raw scrollback delta:
// (a) if the first line contains the prompt string, drop it (prompt echo);
// (b) drop trailing lines that are empty or end in $ # % > after right-trimming spaces;
// (c) join remaining lines with "\n".
func trimDelta(lines []string, prompt string) string {
	// (a) drop prompt-echo first line
	if len(lines) > 0 && strings.Contains(lines[0], prompt) {
		lines = lines[1:]
	}
	// (b) drop trailing blank/shell-prompt lines
	for len(lines) > 0 {
		last := strings.TrimRight(lines[len(lines)-1], " ")
		if last == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		switch last[len(last)-1] {
		case '$', '#', '%', '>':
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	// (c) join
	return strings.Join(lines, "\n")
}
