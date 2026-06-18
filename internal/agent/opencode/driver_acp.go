package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

const acpDriverName = "opencode-acp"

type ACPDriver struct{}

type ACPRuntime struct {
	mu        sync.Mutex
	runMu     sync.Mutex
	spec      agent.Spec
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	scanner   *bufio.Scanner
	stderr    *acpStderrWriter
	state     runtimeState
	readErr   error
	closeOnce sync.Once

	nextID  atomic.Int64
	pending map[int64]chan acpRPCResponse

	sessionID      string
	sessionWorkDir string
	activeSession  string
	activePromptCh chan acpPromptEvent
}

type acpRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type acpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpRPCError    `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type acpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type acpPromptEvent struct {
	Kind  string
	Delta string
	Text  string
	Err   error
}

type runtimeState string

const (
	stateStarting runtimeState = "starting"
	stateReady    runtimeState = "ready"
	stateRunning  runtimeState = "running"
	stateBroken   runtimeState = "broken"
)

const runtimeTypeOpencode = "opencode"

func init() {
	agent.MustRegisterDriver(acpDriverName, func() agent.Driver {
		return NewACPDriver()
	})
}

func NewACPDriver() *ACPDriver {
	return &ACPDriver{}
}

func (d *ACPDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("opencode acp driver requires command")
	}

	cmd := exec.CommandContext(ctx, spec.Command, buildACPArgs(spec.Command, spec.Args, spec.RealCLI)...)
	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		cmd.Dir = workDir
	}
	if env := flattenEnv(spec.Env); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	stderr := &acpStderrWriter{}
	cmd.Stderr = stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("opencode acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("opencode acp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start opencode acp: %w", err)
	}

	runtime := &ACPRuntime{
		spec:    spec,
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		stderr:  stderr,
		state:   stateStarting,
		pending: make(map[int64]chan acpRPCResponse),
	}
	runtime.scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	go runtime.readLoop()

	initCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		initCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	}
	defer cancel()

	if _, err := runtime.rpc(initCtx, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "myclaw",
			"version": "0.1.0",
		},
	}); err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("initialize opencode acp: %w", err)
	}
	if err := runtime.notify("initialized", nil); err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("notify opencode acp initialized: %w", err)
	}

	runtime.mu.Lock()
	runtime.state = stateReady
	runtime.mu.Unlock()
	return runtime, nil
}

func (r *ACPRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return agent.Response{}, fmt.Errorf("opencode acp request prompt is required")
	}

	r.mu.Lock()
	if r.state == stateBroken {
		err := r.readErr
		if err == nil {
			err = fmt.Errorf("opencode acp runtime is broken")
		}
		r.mu.Unlock()
		return agent.Response{}, err
	}
	r.state = stateRunning
	r.mu.Unlock()

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && r.spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.spec.Timeout)
	}
	defer cancel()

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(r.spec.WorkDir)
	}

	sessionID, err := r.ensureSession(runCtx, workDir)
	if err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	promptCh := make(chan acpPromptEvent, 256)
	r.mu.Lock()
	r.activeSession = sessionID
	r.activePromptCh = promptCh
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.activePromptCh == promptCh {
			r.activePromptCh = nil
			r.activeSession = ""
		}
		r.mu.Unlock()
	}()

	go func() {
		_, rpcErr := r.rpc(runCtx, "session/prompt", map[string]any{
			"sessionId": sessionID,
			"text":      prompt,
		})
		if rpcErr != nil {
			select {
			case promptCh <- acpPromptEvent{Kind: "error", Err: rpcErr}:
			default:
			}
		}
	}()

	var parts []string
	for {
		select {
		case <-runCtx.Done():
			err := classifyACPContextError(runCtx.Err())
			r.markBroken(err)
			return agent.Response{}, err
		case evt := <-promptCh:
			if evt.Err != nil {
				r.markBroken(evt.Err)
				return agent.Response{}, fmt.Errorf("opencode acp prompt failed: %w", evt.Err)
			}
			if evt.Delta != "" {
				parts = append(parts, evt.Delta)
			}
			if evt.Text != "" {
				parts = append(parts, evt.Text)
			}
			if evt.Kind == "completed" {
				text := strings.TrimSpace(strings.Join(parts, ""))
				if text == "" {
					err := fmt.Errorf("opencode acp returned empty response")
					r.markBroken(err)
					return agent.Response{}, err
				}
				r.mu.Lock()
				if r.state != stateBroken {
					r.state = stateReady
				}
				r.mu.Unlock()
				return agent.Response{
					Text:        text,
					RuntimeType: runtimeTypeOpencode,
					ExitCode:    0,
					RawOutput:   text,
				}, nil
			}
		}
	}
}

func (r *ACPRuntime) Close() error {
	if r == nil {
		return nil
	}

	var closeErr error
	r.closeOnce.Do(func() {
		r.mu.Lock()
		stdin := r.stdin
		cmd := r.cmd
		r.stdin = nil
		r.cmd = nil
		r.state = stateBroken
		r.readErr = errors.New("opencode acp runtime is closed")
		r.mu.Unlock()

		if stdin != nil {
			_ = stdin.Close()
		}
		if cmd == nil || cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
		if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == -1 {
				return
			}
			var errno syscall.Errno
			if errors.As(err, &errno) && errno == syscall.ECHILD {
				return
			}
			closeErr = err
		}
	})
	return closeErr
}

func buildACPArgs(command string, args []string, realCLI bool) []string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	if base != "opencode" && base != "opencode.exe" && !realCLI {
		return append([]string(nil), args...)
	}

	for _, arg := range args {
		if arg == "acp" {
			return append([]string(nil), args...)
		}
	}

	out := make([]string, 0, len(args)+1)
	out = append(out, "acp")
	out = append(out, args...)
	return out
}

func classifyACPContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("opencode acp timed out: %w", err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("opencode acp canceled: %w", err)
	}
	return err
}

func (r *ACPRuntime) ensureSession(ctx context.Context, workDir string) (string, error) {
	r.mu.Lock()
	sessionID := r.sessionID
	sessionWorkDir := r.sessionWorkDir
	r.mu.Unlock()

	if sessionID != "" && sessionWorkDir == workDir {
		return sessionID, nil
	}

	params := map[string]any{
		"cwd": workDir,
	}
	result, err := r.rpc(ctx, "session/new", params)
	if err != nil {
		return "", err
	}

	var sessionResult struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(result, &sessionResult); err != nil {
		return "", fmt.Errorf("decode opencode acp session/new result: %w", err)
	}
	if sessionResult.Session.ID == "" {
		return "", fmt.Errorf("opencode acp session/new returned empty session id")
	}

	r.mu.Lock()
	r.sessionID = sessionResult.Session.ID
	r.sessionWorkDir = workDir
	r.mu.Unlock()
	return sessionResult.Session.ID, nil
}

func (r *ACPRuntime) notify(method string, params interface{}) error {
	msg := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stdin == nil {
		return fmt.Errorf("opencode acp stdin is closed")
	}
	_, err = fmt.Fprintf(r.stdin, "%s\n", data)
	return err
}

func (r *ACPRuntime) rpc(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := r.nextID.Add(1)
	ch := make(chan acpRPCResponse, 1)

	r.mu.Lock()
	if r.stdin == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("opencode acp stdin is closed")
	}
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}()

	req := acpRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	_, err = fmt.Fprintf(r.stdin, "%s\n", data)
	r.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write opencode acp request: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			message := strings.TrimSpace(resp.Error.Message)
			if message == "" && r.stderr != nil {
				message = strings.TrimSpace(r.stderr.LastError())
			}
			if message == "" {
				message = "unknown ACP error"
			}
			return nil, fmt.Errorf("%s", message)
		}
		return resp.Result, nil
	}
}

func (r *ACPRuntime) readLoop() {
	for r.scanner.Scan() {
		line := strings.TrimSpace(r.scanner.Text())
		if line == "" {
			continue
		}

		var msg acpRPCResponse
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if msg.Method == "" && len(msg.ID) > 0 {
			var id int64
			if err := json.Unmarshal(msg.ID, &id); err == nil {
				r.mu.Lock()
				ch := r.pending[id]
				r.mu.Unlock()
				if ch != nil {
					ch <- msg
				}
			}
			continue
		}

		switch msg.Method {
		case "session/update":
			r.handleSessionUpdate(msg.Params)
		case "session/request_permission":
			r.handlePermissionRequest([]byte(line))
		}
	}

	err := r.scanner.Err()
	if err == nil {
		err = io.EOF
	}
	r.markBroken(fmt.Errorf("opencode acp read failed: %w", err))

	r.mu.Lock()
	pending := make([]chan acpRPCResponse, 0, len(r.pending))
	for _, ch := range r.pending {
		pending = append(pending, ch)
	}
	promptCh := r.activePromptCh
	r.mu.Unlock()

	for _, ch := range pending {
		ch <- acpRPCResponse{Error: &acpRPCError{Message: err.Error()}}
	}
	if promptCh != nil {
		select {
		case promptCh <- acpPromptEvent{Kind: "error", Err: err}:
		default:
		}
	}
}

func (r *ACPRuntime) handlePermissionRequest(raw []byte) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Params struct {
			Options []struct {
				OptionID string `json:"optionId"`
				Kind     string `json:"kind"`
			} `json:"options"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}

	optionID := "allow"
	for _, opt := range req.Params.Options {
		if opt.Kind == "allow" || opt.Kind == "allowOnce" {
			optionID = opt.OptionID
			break
		}
	}

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(req.ID),
		"result": map[string]any{
			"outcome": map[string]any{
				"outcome":  "selected",
				"optionId": optionID,
			},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stdin == nil {
		return
	}
	_, _ = fmt.Fprintf(r.stdin, "%s\n", data)
}

func (r *ACPRuntime) handleSessionUpdate(params json.RawMessage) {
	var payload struct {
		SessionID string `json:"sessionId"`
		Item      struct {
			Type      string `json:"type"`
			Text      string `json:"text,omitempty"`
			Delta     string `json:"delta,omitempty"`
			Completed bool   `json:"completed,omitempty"`
		} `json:"contentItem"`
		ContentItems []struct {
			Type      string `json:"type"`
			Text      string `json:"text,omitempty"`
			Delta     string `json:"delta,omitempty"`
			Completed bool   `json:"completed,omitempty"`
		} `json:"contentItems"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}

	sessionID := payload.SessionID

	if payload.Item.Type != "" {
		r.dispatchPromptEvent(sessionID, payload.Item.Type, payload.Item.Text, payload.Item.Delta, payload.Item.Completed)
	}

	for _, item := range payload.ContentItems {
		r.dispatchPromptEvent(sessionID, item.Type, item.Text, item.Delta, item.Completed)
	}
}

func (r *ACPRuntime) dispatchPromptEvent(sessionID, itemType, text, delta string, completed bool) {
	switch {
	case delta != "":
		r.dispatchDelta(sessionID, delta)
		if completed {
			r.dispatchCompleted(sessionID)
		}
	case text != "" && completed:
		r.dispatchDelta(sessionID, text)
		r.dispatchCompleted(sessionID)
	case completed:
		r.dispatchCompleted(sessionID)
	case text != "":
		r.dispatchText(sessionID, text)
	}
}

func (r *ACPRuntime) dispatchDelta(sessionID, delta string) {
	r.mu.Lock()
	activeSession := r.activeSession
	promptCh := r.activePromptCh
	r.mu.Unlock()

	if promptCh == nil {
		return
	}
	if sessionID != "" && activeSession != "" && sessionID != activeSession {
		return
	}

	select {
	case promptCh <- acpPromptEvent{Delta: delta}:
	default:
	}
}

func (r *ACPRuntime) dispatchText(sessionID, text string) {
	r.mu.Lock()
	activeSession := r.activeSession
	promptCh := r.activePromptCh
	r.mu.Unlock()

	if promptCh == nil {
		return
	}
	if sessionID != "" && activeSession != "" && sessionID != activeSession {
		return
	}

	select {
	case promptCh <- acpPromptEvent{Text: text}:
	default:
	}
}

func (r *ACPRuntime) dispatchCompleted(sessionID string) {
	r.mu.Lock()
	activeSession := r.activeSession
	promptCh := r.activePromptCh
	r.mu.Unlock()

	if promptCh == nil {
		return
	}
	if sessionID != "" && activeSession != "" && sessionID != activeSession {
		return
	}

	select {
	case promptCh <- acpPromptEvent{Kind: "completed"}:
	default:
	}
}

func (r *ACPRuntime) markBroken(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readErr = err
	r.state = stateBroken
}

type acpStderrWriter struct {
	mu   sync.Mutex
	last string
}

func (w *acpStderrWriter) Write(p []byte) (int, error) {
	lines := strings.Split(strings.TrimSpace(string(p)), "\n")
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			w.last = line
		}
	}
	return len(p), nil
}

func (w *acpStderrWriter) LastError() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
