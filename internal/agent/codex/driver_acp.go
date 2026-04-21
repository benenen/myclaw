package codex

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

const acpDriverName = "codex-acp"

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

	threadID      string
	threadWorkDir string
	activeThread  string
	activeTurnCh  chan acpTurnEvent
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

type acpTurnEvent struct {
	Kind  string
	Delta string
	Text  string
	Err   error
}

type acpPermissionRequest struct {
	ID     json.RawMessage `json:"id"`
	Params struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	} `json:"params"`
}

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
		return nil, fmt.Errorf("codex acp driver requires command")
	}

	cmd := exec.CommandContext(ctx, spec.Command, buildACPArgs(spec.Command, spec.Args)...)
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
		return nil, fmt.Errorf("codex acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex acp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex acp: %w", err)
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
		return nil, fmt.Errorf("initialize codex acp: %w", err)
	}
	if err := runtime.notify("initialized", nil); err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("notify codex acp initialized: %w", err)
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
		return agent.Response{}, fmt.Errorf("codex acp request prompt is required")
	}

	r.mu.Lock()
	if r.state == stateBroken {
		err := r.readErr
		if err == nil {
			err = fmt.Errorf("codex acp runtime is broken")
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

	threadID, err := r.ensureThread(runCtx, workDir)
	if err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	turnCh := make(chan acpTurnEvent, 256)
	r.mu.Lock()
	r.activeThread = threadID
	r.activeTurnCh = turnCh
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.activeTurnCh == turnCh {
			r.activeTurnCh = nil
			r.activeThread = ""
		}
		r.mu.Unlock()
	}()

	go func() {
		_, rpcErr := r.rpc(runCtx, "turn/start", map[string]any{
			"threadId":       threadID,
			"approvalPolicy": "never",
			"input": []map[string]any{
				{"type": "text", "text": prompt},
			},
			"sandboxPolicy": map[string]any{"type": "dangerFullAccess"},
			"cwd":           workDir,
		})
		if rpcErr != nil {
			select {
			case turnCh <- acpTurnEvent{Kind: "error", Err: rpcErr}:
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
		case evt := <-turnCh:
			if evt.Err != nil {
				r.markBroken(evt.Err)
				return agent.Response{}, fmt.Errorf("codex acp turn failed: %w", evt.Err)
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
					err := fmt.Errorf("codex acp returned empty response")
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
					RuntimeType: runtimeTypeCodex,
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
		r.readErr = errors.New("codex acp runtime is closed")
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

func buildACPArgs(command string, args []string) []string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	if base != "codex" && base != "codex.exe" {
		return append([]string(nil), args...)
	}

	for _, arg := range args {
		if arg == "app-server" {
			return append([]string(nil), args...)
		}
	}

	out := make([]string, 0, len(args)+1)
	out = append(out, "app-server")
	out = append(out, args...)
	return out
}

func classifyACPContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("codex acp timed out: %w", err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("codex acp canceled: %w", err)
	}
	return err
}

func (r *ACPRuntime) ensureThread(ctx context.Context, workDir string) (string, error) {
	r.mu.Lock()
	threadID := r.threadID
	threadWorkDir := r.threadWorkDir
	r.mu.Unlock()

	if threadID != "" && threadWorkDir == workDir {
		return threadID, nil
	}

	params := map[string]any{
		"approvalPolicy": "never",
		"cwd":            workDir,
		"sandbox":        "danger-full-access",
	}
	result, err := r.rpc(ctx, "thread/start", params)
	if err != nil {
		return "", err
	}

	var threadResult struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &threadResult); err != nil {
		return "", fmt.Errorf("decode codex acp thread/start result: %w", err)
	}
	if threadResult.Thread.ID == "" {
		return "", fmt.Errorf("codex acp thread/start returned empty thread id")
	}

	r.mu.Lock()
	r.threadID = threadResult.Thread.ID
	r.threadWorkDir = workDir
	r.mu.Unlock()
	return threadResult.Thread.ID, nil
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
		return fmt.Errorf("codex acp stdin is closed")
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
		return nil, fmt.Errorf("codex acp stdin is closed")
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
		return nil, fmt.Errorf("write codex acp request: %w", err)
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
		case "turn/approval/request":
			r.handleApprovalRequest([]byte(line))
		case "item/agentMessage/delta":
			r.handleItemDelta(msg.Params)
		case "item/started":
			r.handleItemStarted(msg.Params)
		case "turn/completed":
			r.handleTurnCompleted(msg.Params)
		case "codex/event/agent_message_delta":
			r.handleCodexAgentMessageDelta(msg.Params)
		}
	}

	err := r.scanner.Err()
	if err == nil {
		err = io.EOF
	}
	r.markBroken(fmt.Errorf("codex acp read failed: %w", err))

	r.mu.Lock()
	pending := make([]chan acpRPCResponse, 0, len(r.pending))
	for _, ch := range r.pending {
		pending = append(pending, ch)
	}
	turnCh := r.activeTurnCh
	r.mu.Unlock()

	for _, ch := range pending {
		ch <- acpRPCResponse{Error: &acpRPCError{Message: err.Error()}}
	}
	if turnCh != nil {
		select {
		case turnCh <- acpTurnEvent{Kind: "error", Err: err}:
		default:
		}
	}
}

func (r *ACPRuntime) handleApprovalRequest(raw []byte) {
	var req acpPermissionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}

	optionID := "allow"
	for _, option := range req.Params.Options {
		if option.Kind == "allow" {
			optionID = option.OptionID
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

func (r *ACPRuntime) handleItemDelta(params json.RawMessage) {
	var payload struct {
		ThreadID string `json:"threadId"`
		Delta    string `json:"delta"`
	}
	if err := json.Unmarshal(params, &payload); err != nil || payload.Delta == "" {
		return
	}
	r.dispatchTurnEvent(payload.ThreadID, acpTurnEvent{Delta: payload.Delta})
}

func (r *ACPRuntime) handleItemStarted(params json.RawMessage) {
	var payload struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}
	if payload.Item.Type != "agentMessage" {
		return
	}
	for _, content := range payload.Item.Content {
		if content.Type == "text" && content.Text != "" {
			r.dispatchTurnEvent(payload.ThreadID, acpTurnEvent{Text: content.Text})
		}
	}
}

func (r *ACPRuntime) handleTurnCompleted(params json.RawMessage) {
	var payload struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}
	r.dispatchTurnEvent(payload.ThreadID, acpTurnEvent{Kind: "completed"})
}

func (r *ACPRuntime) handleCodexAgentMessageDelta(params json.RawMessage) {
	var payload struct {
		ThreadID string `json:"threadId"`
		Msg      struct {
			Delta string `json:"delta"`
		} `json:"msg"`
	}
	if err := json.Unmarshal(params, &payload); err != nil || payload.Msg.Delta == "" {
		return
	}
	r.dispatchTurnEvent(payload.ThreadID, acpTurnEvent{Delta: payload.Msg.Delta})
}

func (r *ACPRuntime) dispatchTurnEvent(threadID string, evt acpTurnEvent) {
	r.mu.Lock()
	activeThread := r.activeThread
	turnCh := r.activeTurnCh
	r.mu.Unlock()

	if turnCh == nil {
		return
	}
	if threadID != "" && activeThread != "" && threadID != activeThread {
		return
	}

	select {
	case turnCh <- evt:
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
