package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ---- I/O types (jsonschema-tagged for the MCP SDK) ----

type LsInput struct{}
type Session struct {
	Name     string `json:"name"`
	Attached bool   `json:"attached"`
	IdleMs   int64  `json:"idle_ms"`
	Title    string `json:"title"`
}
type LsOutput struct {
	Sessions []Session `json:"sessions" jsonschema:"the live boo sessions"`
}

type NewInput struct {
	Name    string   `json:"name,omitempty" jsonschema:"session name (a unique prefix is enough elsewhere)"`
	Command []string `json:"command,omitempty" jsonschema:"command + args to run; default the user's shell"`
	Rows    int      `json:"rows,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Cwd     string   `json:"cwd,omitempty" jsonschema:"working directory; must already exist"`
}
type NewOutput struct {
	Name string `json:"name" jsonschema:"the created session name"`
}

type SendInput struct {
	Name  string `json:"name"`
	Text  string `json:"text,omitempty" jsonschema:"literal text to type (no implicit newline)"`
	Enter bool   `json:"enter,omitempty" jsonschema:"append Enter after the text"`
	Keys  string `json:"keys,omitempty" jsonschema:"comma-separated named keys e.g. Enter,C-c,Up (mutually exclusive with text)"`
}
type SendOutput struct {
	Ok bool `json:"ok"`
}

type PeekInput struct {
	Name       string `json:"name"`
	Scrollback bool   `json:"scrollback,omitempty" jsonschema:"include full scrollback history"`
}
type Cursor struct {
	Row int `json:"row"`
	Col int `json:"col"`
}
type PeekOutput struct {
	Session string `json:"session"`
	Title   string `json:"title"`
	Rows    int    `json:"rows"`
	Cols    int    `json:"cols"`
	Cursor  Cursor `json:"cursor"`
	Screen  string `json:"screen"`
}

type WaitInput struct {
	Name    string `json:"name"`
	Mode    string `json:"mode" jsonschema:"one of: text, idle"`
	Text    string `json:"text,omitempty" jsonschema:"substring to wait for (mode=text)"`
	Timeout string `json:"timeout,omitempty" jsonschema:"duration like 2s, 1m (default 30s)"`
}
type WaitOutput struct {
	Matched bool `json:"matched" jsonschema:"true if the condition was met, false on timeout"`
}

type KillInput struct {
	Name string `json:"name,omitempty"`
	All  bool   `json:"all,omitempty" jsonschema:"kill every session"`
}
type KillOutput struct {
	Ok bool `json:"ok"`
}

// ---- argv builders (pure; validation lives here) ----

func argsForLs(LsInput) ([]string, error) { return []string{"ls", "--json"}, nil }

func argsForNew(in NewInput) ([]string, error) {
	args := []string{"new"}
	if in.Name != "" {
		args = append(args, in.Name)
	}
	args = append(args, "-d")
	if in.Rows > 0 {
		args = append(args, "--rows", strconv.Itoa(in.Rows))
	}
	if in.Cols > 0 {
		args = append(args, "--cols", strconv.Itoa(in.Cols))
	}
	if in.Cwd != "" {
		args = append(args, "--cwd", in.Cwd)
	}
	if len(in.Command) > 0 {
		args = append(args, "--")
		args = append(args, in.Command...)
	}
	return args, nil
}

func argsForSend(in SendInput) ([]string, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	hasText, hasKeys := in.Text != "", in.Keys != ""
	if hasText == hasKeys {
		return nil, fmt.Errorf("exactly one of text or keys is required")
	}
	args := []string{"send", in.Name}
	if hasText {
		args = append(args, "--text", in.Text)
		if in.Enter {
			args = append(args, "--enter")
		}
	} else {
		args = append(args, "--key", in.Keys)
	}
	return args, nil
}

func argsForPeek(in PeekInput) ([]string, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	args := []string{"peek", in.Name, "--json"}
	if in.Scrollback {
		args = append(args, "--scrollback")
	}
	return args, nil
}

func argsForWait(in WaitInput) ([]string, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	args := []string{"wait", in.Name}
	switch in.Mode {
	case "text":
		if in.Text == "" {
			return nil, fmt.Errorf("mode=text requires text")
		}
		args = append(args, "--text", in.Text)
	case "idle":
		args = append(args, "--idle")
	default:
		return nil, fmt.Errorf("mode must be text or idle")
	}
	if in.Timeout != "" {
		args = append(args, "--timeout", in.Timeout)
	}
	return args, nil
}

func argsForKill(in KillInput) ([]string, error) {
	hasName, hasAll := in.Name != "", in.All
	if hasName == hasAll {
		return nil, fmt.Errorf("exactly one of name or all is required")
	}
	if hasAll {
		return []string{"kill", "--all"}, nil
	}
	return []string{"kill", in.Name}, nil
}

// runBoo is the single exec seam; handlers call it, tests stub it.
var runBoo = func(ctx context.Context, args ...string) (stdout []byte, stderr []byte, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "boo", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out.Bytes(), errb.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return out.Bytes(), errb.Bytes(), -1, fmt.Errorf("boo not available: %w", err)
	}
	return out.Bytes(), errb.Bytes(), 0, nil
}

// booError maps a non-success boo exit code to a tool error. Returns nil for 0.
// Exit 4 (wait timeout) is handled by runWait directly and not passed here.
func booError(name string, exitCode int, stderr []byte) error {
	switch exitCode {
	case 0:
		return nil
	case 3:
		return fmt.Errorf("no such session: %s", name)
	default:
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = fmt.Sprintf("boo exited %d", exitCode)
		}
		return fmt.Errorf("boo error: %s", msg)
	}
}

func runLs(ctx context.Context, in LsInput) (LsOutput, error) {
	args, err := argsForLs(in)
	if err != nil {
		return LsOutput{}, err
	}
	out, errb, code, err := runBoo(ctx, args...)
	if err != nil {
		return LsOutput{}, err
	}
	if e := booError("", code, errb); e != nil {
		return LsOutput{}, e
	}
	sessions := []Session{}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &sessions); err != nil {
			return LsOutput{}, fmt.Errorf("parse ls --json: %w", err)
		}
	}
	return LsOutput{Sessions: sessions}, nil
}

func runNew(ctx context.Context, in NewInput) (NewOutput, error) {
	args, err := argsForNew(in)
	if err != nil {
		return NewOutput{}, err
	}
	out, errb, code, err := runBoo(ctx, args...)
	if err != nil {
		return NewOutput{}, err
	}
	if e := booError(in.Name, code, errb); e != nil {
		return NewOutput{}, e
	}
	return NewOutput{Name: strings.TrimSpace(string(out))}, nil
}

func runSend(ctx context.Context, in SendInput) (SendOutput, error) {
	args, err := argsForSend(in)
	if err != nil {
		return SendOutput{}, err
	}
	_, errb, code, err := runBoo(ctx, args...)
	if err != nil {
		return SendOutput{}, err
	}
	if e := booError(in.Name, code, errb); e != nil {
		return SendOutput{}, e
	}
	return SendOutput{Ok: true}, nil
}

func runPeek(ctx context.Context, in PeekInput) (PeekOutput, error) {
	args, err := argsForPeek(in)
	if err != nil {
		return PeekOutput{}, err
	}
	out, errb, code, err := runBoo(ctx, args...)
	if err != nil {
		return PeekOutput{}, err
	}
	if e := booError(in.Name, code, errb); e != nil {
		return PeekOutput{}, e
	}
	var po PeekOutput
	if err := json.Unmarshal(out, &po); err != nil {
		return PeekOutput{}, fmt.Errorf("parse peek --json: %w", err)
	}
	return po, nil
}

func runWait(ctx context.Context, in WaitInput) (WaitOutput, error) {
	args, err := argsForWait(in)
	if err != nil {
		return WaitOutput{}, err
	}
	_, errb, code, err := runBoo(ctx, args...)
	if err != nil {
		return WaitOutput{}, err
	}
	if code == 4 { // timeout: a normal result, not an error
		return WaitOutput{Matched: false}, nil
	}
	if e := booError(in.Name, code, errb); e != nil {
		return WaitOutput{}, e
	}
	return WaitOutput{Matched: true}, nil
}

func runKill(ctx context.Context, in KillInput) (KillOutput, error) {
	args, err := argsForKill(in)
	if err != nil {
		return KillOutput{}, err
	}
	_, errb, code, err := runBoo(ctx, args...)
	if err != nil {
		return KillOutput{}, err
	}
	if e := booError(in.Name, code, errb); e != nil {
		return KillOutput{}, e
	}
	return KillOutput{Ok: true}, nil
}
