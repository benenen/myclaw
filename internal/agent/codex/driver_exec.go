package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

const (
	execDriverName   = "codex-exec"
	runtimeTypeCodex = "codex"
)

func init() {
	agent.MustRegisterDriver(execDriverName, func() agent.Driver {
		return NewExecDriver()
	})
}

type ExecDriver struct{}

type ExecRuntime struct {
	spec agent.Spec
}

func (r *ExecRuntime) Close() error {
	return nil
}

type execStreamEvent struct {
	Type    string           `json:"type"`
	Item    *execStreamItem  `json:"item,omitempty"`
	Message string           `json:"message,omitempty"`
	Error   *execStreamError `json:"error,omitempty"`
}

type execStreamError struct {
	Message string `json:"message,omitempty"`
}

type execStreamItem struct {
	Text string `json:"text,omitempty"`
}

func NewExecDriver() *ExecDriver {
	return &ExecDriver{}
}

func (d *ExecDriver) Init(_ context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("codex exec driver requires command")
	}
	return &ExecRuntime{spec: spec}, nil
}

func (r *ExecRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return agent.Response{}, fmt.Errorf("codex exec request prompt is required")
	}

	start := time.Now()
	slog.Info("agent turn start", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "prompt_len", len(prompt))
	slog.Debug("agent turn prompt", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "prompt", prompt)

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && r.spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.spec.Timeout)
	}
	defer cancel()

	args := append([]string(nil), r.spec.Args...)
	args = append(args, "exec", "--json", "--skip-git-repo-check", "resume", "--last", prompt)

	slog.Info("agent cli launching", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "command", r.spec.Command, "args", agent.SummarizeArgs(args), "real_cli", r.spec.RealCLI)
	cmd := exec.CommandContext(runCtx, r.spec.Command, args...)
	if workDir := strings.TrimSpace(req.WorkDir); workDir != "" {
		cmd.Dir = workDir
	} else if strings.TrimSpace(r.spec.WorkDir) != "" {
		cmd.Dir = r.spec.WorkDir
	}
	if env := flattenEnv(r.spec.Env); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	duration := time.Since(start)
	rawOutput := stdout.String()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if runCtx.Err() != nil {
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				err := fmt.Errorf("codex exec timed out: %w", runCtx.Err())
				slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "error", err)
				return agent.Response{}, err
			}
			if errors.Is(runCtx.Err(), context.Canceled) {
				err := fmt.Errorf("codex exec canceled: %w", runCtx.Err())
				slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "error", err)
				return agent.Response{}, err
			}
			slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "error", runCtx.Err())
			return agent.Response{}, runCtx.Err()
		}
		message := extractExecFailureMessage(rawOutput, strings.TrimSpace(stderr.String()))
		if message == "" {
			message = runErr.Error()
		}
		err := fmt.Errorf("codex exec failed: %s", message)
		slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "error", err)
		return agent.Response{Text: message, RuntimeType: runtimeTypeCodex, ExitCode: exitCode, Duration: duration, RawOutput: rawOutput}, err
	}

	text, parseErr := lastCompletedItemText(rawOutput)
	if parseErr != nil {
		slog.Error("agent turn failed", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "error", parseErr)
		return agent.Response{RuntimeType: runtimeTypeCodex, ExitCode: exitCode, Duration: duration, RawOutput: rawOutput}, parseErr
	}

	slog.Info("agent turn done", "bot_id", r.spec.BotID, "runtime", runtimeTypeCodex, "duration", time.Since(start), "exit_code", exitCode)
	return agent.Response{
		Text:        text,
		RuntimeType: runtimeTypeCodex,
		ExitCode:    exitCode,
		Duration:    duration,
		RawOutput:   rawOutput,
	}, nil
}

func extractExecFailureMessage(raw, stderr string) string {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event execStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type == "turn.failed" && event.Error != nil {
			if message := strings.TrimSpace(event.Error.Message); message != "" {
				return message
			}
		}
	}
	if message := strings.TrimSpace(stderr); message != "" {
		return message
	}
	return strings.TrimSpace(raw)
}

func lastCompletedItemText(raw string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var last string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event execStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return "", fmt.Errorf("decode codex exec output: %w", err)
		}
		if event.Type != "item.completed" || event.Item == nil {
			continue
		}
		if text := strings.TrimSpace(event.Item.Text); text != "" {
			last = text
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read codex exec output: %w", err)
	}
	if last == "" {
		return "", fmt.Errorf("codex exec output missing completed item text")
	}
	return last, nil
}
