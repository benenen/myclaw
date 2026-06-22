package main

import (
	"context"
	"fmt"
	"strconv"
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

// runBoo is the single exec seam; handlers (Task 2) call it, tests stub it.
var runBoo = func(ctx context.Context, args ...string) (stdout []byte, stderr []byte, exitCode int, err error) {
	return nil, nil, 0, fmt.Errorf("runBoo not implemented") // replaced in Task 2
}
