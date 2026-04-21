package tmux

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/GianlucaP106/gotmux/gotmux"
	"github.com/benenen/myclaw/internal/agent"
)

type fakeSessionPaneLister struct {
	panes []*gotmux.Pane
	err   error
}

func (f fakeSessionPaneLister) ListPanes() ([]*gotmux.Pane, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.panes, nil
}

func TestFirstSessionPaneUsesFirstAvailablePane(t *testing.T) {
	want := &gotmux.Pane{Index: 1}

	got, err := firstSessionPane(fakeSessionPaneLister{
		panes: []*gotmux.Pane{want, {Index: 2}},
	}, "myclaw-codex-test")
	if err != nil {
		t.Fatalf("firstSessionPane() error = %v", err)
	}
	if got != want {
		t.Fatalf("firstSessionPane() = %#v, want %#v", got, want)
	}
}

func TestFirstSessionPaneReturnsSessionNameWhenNoPanesExist(t *testing.T) {
	_, err := firstSessionPane(fakeSessionPaneLister{}, "myclaw-codex-test")
	if err == nil {
		t.Fatal("expected error when no panes exist")
	}
	if !strings.Contains(err.Error(), `tmux session "myclaw-codex-test" has no panes`) {
		t.Fatalf("unexpected error = %v", err)
	}
}

func TestFirstSessionPaneWrapsListPanesFailure(t *testing.T) {
	_, err := firstSessionPane(fakeSessionPaneLister{err: errors.New("boom")}, "myclaw-codex-test")
	if err == nil {
		t.Fatal("expected list panes error")
	}
	if !strings.Contains(err.Error(), `start tmux session "myclaw-codex-test": boom`) {
		t.Fatalf("unexpected error = %v", err)
	}
}

func TestValidateStartupSpecReportsMissingExecutableAndWorkDir(t *testing.T) {
	spec := agent.Spec{
		Command: "/root/.nvm/versions/node/v20.19.2/bin/codex -V",
		WorkDir: "/root/.myclaw/bots/missing/workspace",
	}

	err := validateStartupSpec(spec)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), `tmux executable "/root/.nvm/versions/node/v20.19.2/bin/codex" not found`) {
		t.Fatalf("unexpected error = %v", err)
	}
	if !strings.Contains(err.Error(), `tmux workdir "/root/.myclaw/bots/missing/workspace" does not exist`) {
		t.Fatalf("unexpected error = %v", err)
	}
}

func TestValidateStartupSpecAcceptsExistingExecutableAndWorkDir(t *testing.T) {
	workDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}

	err = validateStartupSpec(agent.Spec{
		Command: exe + " -test.run TestHelperProcess",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("validateStartupSpec() error = %v", err)
	}
}

func TestWrapStartErrorIncludesCommandAndWorkDir(t *testing.T) {
	spec := agent.Spec{
		Command: "/root/.nvm/versions/node/v22.16.0/bin/codex -c 'notify=[\"myclaw\", \"notify\", \"codex\", \"test\"]'",
		WorkDir: "/root/.myclaw/bots/bot_1/workspace",
	}

	err := wrapStartError("myclaw-codex-test", spec, errors.New("failed to list panes"))
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	if !strings.Contains(err.Error(), `start tmux session "myclaw-codex-test": failed to list panes`) {
		t.Fatalf("unexpected error = %v", err)
	}
	if !strings.Contains(err.Error(), `command="/root/.nvm/versions/node/v22.16.0/bin/codex -c 'notify=[\"myclaw\", \"notify\", \"codex\", \"test\"]'"`) {
		t.Fatalf("unexpected error = %v", err)
	}
	if !strings.Contains(err.Error(), `workdir="/root/.myclaw/bots/bot_1/workspace"`) {
		t.Fatalf("unexpected error = %v", err)
	}
}

func TestWithTMUXDiagnosticsAppendsDiagnosticText(t *testing.T) {
	err := withTMUXDiagnostics(errors.New("failed to list panes"), `has-session exit=1 out="can't find session"`)
	if err == nil {
		t.Fatal("expected annotated error")
	}
	if !strings.Contains(err.Error(), `failed to list panes; tmux_diag=has-session exit=1 out="can't find session"`) {
		t.Fatalf("unexpected error = %v", err)
	}
}

func TestNewSessionArgsPreservesRawShellCommand(t *testing.T) {
	spec := agent.Spec{
		Command: `/root/.nvm/versions/node/v20.19.2/bin/codex -c 'notify=["myclaw", "notify", "codex", "test"]' -c 'projects."/root/.myclaw/bots/bot_1/workspace".trust_level="trusted"'`,
		WorkDir: "/root/.myclaw/bots/bot_1/workspace",
	}

	got := newSessionArgs("myclaw-codex-test", spec)
	want := []string{
		"new-session",
		"-d",
		"-s", "myclaw-codex-test",
		"-c", "/root/.myclaw/bots/bot_1/workspace",
		`/root/.nvm/versions/node/v20.19.2/bin/codex -c 'notify=["myclaw", "notify", "codex", "test"]' -c 'projects."/root/.myclaw/bots/bot_1/workspace".trust_level="trusted"'`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newSessionArgs() = %#v, want %#v", got, want)
	}
}
