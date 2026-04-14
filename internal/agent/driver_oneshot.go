package agent

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

type OneshotDriver struct{}

type OneshotRuntime struct {
	spec Spec
}

func init() {
	MustRegisterDriver("oneshot", func() Driver {
		return NewOneshotDriver()
	})
}

func NewOneshotDriver() *OneshotDriver {
	return &OneshotDriver{}
}

func (d *OneshotDriver) Init(_ context.Context, spec Spec) (SessionRuntime, error) {
	return &OneshotRuntime{spec: cloneSpec(spec)}, nil
}

func (r *OneshotRuntime) Run(ctx context.Context, req Request) (Response, error) {
	runCtx := ctx
	cancel := func() {}
	if r.spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.spec.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.spec.Command, r.spec.Args...)
	if r.spec.WorkDir != "" {
		cmd.Dir = r.spec.WorkDir
	}
	if env := flattenEnv(r.spec.Env); len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	cmd.Stdin = strings.NewReader(req.Prompt)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	err := cmd.Run()

	stdoutText := normalizeOutput(stdout.String())
	stderrText := normalizeOutput(stderr.String())
	rawOutput := stdoutText
	if stderrText != "" {
		if rawOutput != "" {
			rawOutput += "\n"
		}
		rawOutput += stderrText
	}

	resp := Response{
		Text:      stdoutText,
		Duration:  time.Since(startedAt),
		RawOutput: rawOutput,
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		resp.ExitCode = exitErr.ExitCode()
	}
	if err != nil {
		if runCtx.Err() != nil {
			return resp, runCtx.Err()
		}
		return resp, err
	}
	if cmd.ProcessState != nil {
		resp.ExitCode = cmd.ProcessState.ExitCode()
	}
	return resp, nil
}

func flattenEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func normalizeOutput(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimRight(text, "\n")
}
