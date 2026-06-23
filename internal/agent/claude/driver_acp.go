package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

const (
	acpDriverName     = "claude-acp"
	runtimeTypeClaude = "claude"
)

type runtimeState string

const (
	stateReady   runtimeState = "ready"
	stateRunning runtimeState = "running"
	stateBroken  runtimeState = "broken"
)

// ACPDriver runs Claude Code as a single long-lived process and talks to it
// over stdin/stdout using its stream-json protocol. One process serves all
// turns for a bot session, so multi-turn context is kept in-process by Claude.
type ACPDriver struct{}

type ACPRuntime struct {
	mu    sync.Mutex
	runMu sync.Mutex

	spec      agent.Spec
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stderr    *acpStderrWriter
	state     runtimeState
	readErr   error
	closeOnce sync.Once
	sessionID string

	activeTurnCh   chan acpTurnEvent
	activeProgress func(agent.ProgressEvent)
}

// getSessionID returns the session id captured from the claude init event.
func (r *ACPRuntime) getSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

// acpTurnEvent carries one piece of progress for the in-flight turn from the
// read loop to Run.
type acpTurnEvent struct {
	done    bool
	text    string
	isError bool
	err     error
}

// claudeStreamEvent is one line of Claude Code's --output-format stream-json.
type claudeStreamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Message   *struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message,omitempty"`
}

func init() {
	agent.MustRegisterDriver(acpDriverName, func() agent.Driver {
		return NewACPDriver()
	})
}

// NewACPDriver creates a new Claude Code stream-json driver.
func NewACPDriver() *ACPDriver {
	return &ACPDriver{}
}

// Init starts the long-lived Claude Code process. Claude only emits its
// system/init event once it begins handling the first user message, so the
// runtime is marked ready as soon as the process starts; a process that died
// or failed to authenticate surfaces as an error on the first Run.
func (d *ACPDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("claude acp driver requires command")
	}

	acpArgs := buildACPArgs(spec.Command, spec.Args, spec.RealCLI, spec.ResumeSessionID)
	slog.Info("agent cli launching", "bot_id", spec.BotID, "runtime", runtimeTypeClaude, "command", spec.Command, "args", agent.SummarizeArgs(acpArgs), "real_cli", spec.RealCLI)
	cmd := exec.CommandContext(ctx, spec.Command, acpArgs...)
	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		cmd.Dir = workDir
	}
	// claude refuses to bypass permissions while running as root unless it
	// believes it is sandboxed. myclaw runs claude as root in a container, so set
	// IS_SANDBOX=1 (the documented escape) — without it, both the
	// --dangerously-skip-permissions flag and permissions.defaultMode=bypassPermissions
	// are rejected with "cannot be used with root/sudo". spec.Env is applied last so
	// an operator-supplied value can still override.
	cmd.Env = append(os.Environ(), "IS_SANDBOX=1")
	cmd.Env = append(cmd.Env, flattenEnv(spec.Env)...)

	stderr := &acpStderrWriter{}
	cmd.Stderr = stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude acp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude acp: %w", err)
	}
	slog.InfoContext(ctx, "agent process started", "cmd", cmd.String())

	runtime := &ACPRuntime{
		spec:   spec,
		cmd:    cmd,
		stdin:  stdin,
		stderr: stderr,
		state:  stateReady,
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	go runtime.readLoop(scanner)

	return runtime, nil
}

// Run sends one user turn to the running process and waits for its result.
func (r *ACPRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return agent.Response{}, fmt.Errorf("claude acp request prompt is required")
	}

	r.mu.Lock()
	if r.state == stateBroken {
		err := r.readErr
		if err == nil {
			err = fmt.Errorf("claude acp runtime is broken")
		}
		r.mu.Unlock()
		return agent.Response{}, err
	}
	r.state = stateRunning
	turnCh := make(chan acpTurnEvent, 256)
	r.activeTurnCh = turnCh
	r.activeProgress = req.OnProgress
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.activeTurnCh == turnCh {
			r.activeTurnCh = nil
			r.activeProgress = nil
		}
		r.mu.Unlock()
	}()

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && r.spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.spec.Timeout)
	}
	defer cancel()

	if err := r.send(prompt); err != nil {
		r.markBroken(err)
		return agent.Response{}, err
	}

	start := time.Now()
	slog.Info("agent turn start", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "prompt_len", len(prompt))
	slog.Debug("agent turn prompt", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "prompt", prompt)
	for {
		select {
		case <-runCtx.Done():
			err := classifyContextError(runCtx.Err())
			r.markBroken(err)
			slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "error", err)
			return agent.Response{}, err
		case evt := <-turnCh:
			slog.Debug("agent turn event", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "done", evt.done)
			if evt.err != nil {
				r.markBroken(evt.err)
				err := fmt.Errorf("claude acp turn failed: %w", evt.err)
				slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "error", err)
				return agent.Response{}, err
			}
			if !evt.done {
				continue
			}
			text := strings.TrimSpace(evt.text)
			if evt.isError {
				if text == "" {
					text = "claude acp returned an error"
				}
				r.mu.Lock()
				if r.state != stateBroken {
					r.state = stateReady
				}
				r.mu.Unlock()
				err := fmt.Errorf("claude acp turn error: %s", text)
				slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "error", err)
				return agent.Response{
					Text:        text,
					RuntimeType: runtimeTypeClaude,
					ExitCode:    1,
					Duration:    time.Since(start),
					RawOutput:   text,
					SessionID:   r.getSessionID(),
				}, err
			}
			r.mu.Lock()
			if r.state != stateBroken {
				r.state = stateReady
			}
			r.mu.Unlock()
			slog.Info("agent turn done", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "duration", time.Since(start))
			return agent.Response{
				Text:        text,
				RuntimeType: runtimeTypeClaude,
				ExitCode:    0,
				Duration:    time.Since(start),
				RawOutput:   text,
				SessionID:   r.getSessionID(),
			}, nil
		}
	}
}

// Close stops the long-lived process and tears down the pipes.
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
		if r.readErr == nil {
			r.readErr = errors.New("claude acp runtime is closed")
		}
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

// send writes a single user message line to the process stdin.
func (r *ACPRuntime) send(prompt string) error {
	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": prompt},
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stdin == nil {
		return fmt.Errorf("claude acp stdin is closed")
	}
	if _, err := fmt.Fprintf(r.stdin, "%s\n", data); err != nil {
		return fmt.Errorf("write claude acp request: %w", err)
	}
	return nil
}

// readLoop parses every stdout line and routes turn results to Run.
func (r *ACPRuntime) readLoop(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		r.handleLine(line)
	}

	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	if last := strings.TrimSpace(r.stderr.LastError()); last != "" {
		err = fmt.Errorf("claude acp read failed: %w (stderr: %s)", err, last)
	} else {
		err = fmt.Errorf("claude acp read failed: %w", err)
	}
	r.markBroken(err)
	r.dispatchTurnEvent(acpTurnEvent{err: err})
}

func (r *ACPRuntime) handleLine(line string) {
	var evt claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return
	}

	if evt.Type == "system" && evt.Subtype == "init" && evt.SessionID != "" {
		r.mu.Lock()
		if r.sessionID == "" {
			r.sessionID = evt.SessionID
			r.mu.Unlock()
			slog.Info("agent session captured", "bot_id", r.spec.BotID, "runtime", runtimeTypeClaude, "session_id", evt.SessionID)
		} else {
			r.mu.Unlock()
		}
	}

	if evt.Type == "assistant" && evt.Message != nil {
		for _, blk := range evt.Message.Content {
			if blk.Type != "tool_use" || blk.Name == "" {
				continue
			}
			var input map[string]any
			_ = json.Unmarshal(blk.Input, &input)
			r.dispatchProgress(agent.ProgressEvent{
				Kind:   "tool",
				Tool:   blk.Name,
				Target: agent.TargetFromInput(blk.Name, input),
			})
		}
	}

	if evt.Type == "result" {
		r.dispatchTurnEvent(acpTurnEvent{
			done:    true,
			text:    evt.Result,
			isError: evt.IsError,
		})
	}
}

func (r *ACPRuntime) dispatchProgress(ev agent.ProgressEvent) {
	r.mu.Lock()
	fn := r.activeProgress
	r.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func (r *ACPRuntime) dispatchTurnEvent(evt acpTurnEvent) {
	r.mu.Lock()
	turnCh := r.activeTurnCh
	r.mu.Unlock()
	if turnCh == nil {
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
	if r.readErr == nil {
		r.readErr = err
	}
	r.state = stateBroken
}

// isClaudeCommand reports whether the command is the real claude binary, used
// to gate flag injection so test stubs run with their args verbatim.
func isClaudeCommand(command string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	return base == "claude" || base == "claude.exe"
}

// buildACPArgs assembles the flags required for a persistent stream-json
// session, appending any operator-supplied args. The required flags are only
// injected for the real claude binary so test stubs receive their args verbatim.
// When resumeSessionID is non-empty the real-binary path appends --resume <id>.
func buildACPArgs(command string, extra []string, realCLI bool, resumeSessionID string) []string {
	if !isClaudeCommand(command) && !realCLI {
		return append([]string(nil), extra...)
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	return append(args, extra...)
}

func classifyContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("claude acp timed out: %w", err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("claude acp canceled: %w", err)
	}
	return err
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
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
