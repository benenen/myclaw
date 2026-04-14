package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
)

func TestNormalizeOutputStripsANSI(t *testing.T) {
	got := normalizeOutput("\x1b[31mhello\x1b[0m\r\n")
	if got != "hello\n" {
		t.Fatalf("normalizeOutput() = %q", got)
	}
}

func TestNormalizeOutputStripsOSCSequence(t *testing.T) {
	got := normalizeOutput("prefix\x1b]0;window title\x07suffix")
	if got != "prefixsuffix" {
		t.Fatalf("normalizeOutput() = %q", got)
	}
}

func TestNormalizeOutputStripsStringControlSequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "dcs bel", input: "prefix\x1bPpayload\x07suffix"},
		{name: "apc st", input: "prefix\x1b_payload\x1b\\suffix"},
		{name: "pm bel", input: "prefix\x1b^payload\x07suffix"},
		{name: "sos st", input: "prefix\x1bXpayload\x1b\\suffix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOutput(tt.input)
			if got != "prefixsuffix" {
				t.Fatalf("normalizeOutput() = %q", got)
			}
		})
	}
}

func TestNormalizeOutputStripsSingleCharacterEscapeSequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "save cursor", input: "prefix\x1b7suffix"},
		{name: "restore cursor", input: "prefix\x1b8suffix"},
		{name: "index", input: "prefix\x1bDsuffix"},
		{name: "next line", input: "prefix\x1bEsuffix"},
		{name: "reverse index", input: "prefix\x1bMsuffix"},
		{name: "string terminator", input: "prefix\x1b\\suffix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOutput(tt.input)
			if got != "prefixsuffix" {
				t.Fatalf("normalizeOutput() = %q", got)
			}
		})
	}
}

func TestNormalizeOutputStripsTwoByteCharsetEscapeSequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "designate UK", input: "prefix\x1b(Asuffix"},
		{name: "designate ASCII", input: "prefix\x1b(Bsuffix"},
		{name: "designate line drawing", input: "prefix\x1b(0suffix"},
		{name: "designate alternate charset", input: "prefix\x1b)0suffix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOutput(tt.input)
			if got != "prefixsuffix" {
				t.Fatalf("normalizeOutput() = %q", got)
			}
		})
	}
}

func TestNormalizeOutput(t *testing.T) {
	got := normalizeOutput("a\r\nb\rc")
	if got != "a\nc" {
		t.Fatalf("normalizeOutput() = %q", got)
	}
}

func TestNormalizeOutputHandlesSplitOSCSequence(t *testing.T) {
	var out bytes.Buffer
	var sanitizer terminalSanitizer
	got1 := sanitizer.Write(&out, []byte("prefix\x1b]0;window"))
	got2 := sanitizer.Write(&out, []byte(" title\x07suffix"))
	if got1 != "prefix" || got2 != "suffix" || out.String() != "prefixsuffix" {
		t.Fatalf("stream sanitize got1=%q got2=%q full=%q", got1, got2, out.String())
	}
}

func TestNormalizeOutputHandlesCarriageReturnRewrite(t *testing.T) {
	var out bytes.Buffer
	var sanitizer terminalSanitizer
	sanitizer.Write(&out, []byte("T\rTi\rTip\rTip: hello\n"))
	if got := out.String(); got != "Tip: hello\n" {
		t.Fatalf("stream sanitize = %q", got)
	}
}

func TestPromptIndexOnOwnLine(t *testing.T) {
	text := "line 1\nstatus codex> still output\ncodex>\n"
	idx, ok := promptIndexOnOwnLine(text, "codex>")
	if !ok {
		t.Fatal("expected prompt on own line")
	}
	if got := text[idx:]; !strings.HasPrefix(got, "codex>") {
		t.Fatalf("promptIndexOnOwnLine() pointed to %q", got)
	}
}

func TestPreferRunErrorSuppressesTerminalEOF(t *testing.T) {
	if err := preferRunError(context.Background(), io.EOF); err != nil {
		t.Fatalf("preferRunError() = %v, want nil before context done", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := preferRunError(ctx, io.EOF); err != nil {
		t.Fatalf("preferRunError() = %v, want nil for terminal EOF", err)
	}
}

func TestWaitRunCompletionStateIgnoresBrokenWithoutReadError(t *testing.T) {
	if err := waitRunCompletionState(nil, stateBroken); err != nil {
		t.Fatalf("waitRunCompletionState() = %v, want nil", err)
	}
}

func TestWaitRunCompletionStateReturnsReadError(t *testing.T) {
	want := io.EOF
	if err := waitRunCompletionState(want, stateBroken); !errors.Is(err, want) {
		t.Fatalf("waitRunCompletionState() = %v, want %v", err, want)
	}
}

func TestExtractRunResultWaitsForPrompt(t *testing.T) {
	text := strings.Join([]string{
		"answer",
		"status codex> still output",
	}, "\n")

	completed, _, ambiguous := extractRunResult(text, 0, "codex>", "")
	if completed || ambiguous {
		t.Fatalf("extractRunResult() = completed %v ambiguous %v, want incomplete", completed, ambiguous)
	}
}

func TestExtractRunResultMatchesTranscript(t *testing.T) {
	text := strings.Join([]string{
		"assistant response: say hello",
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, "codex>", "say hello")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v text=%q raw=%q", completed, ambiguous, result.text, result.raw)
	}
	if result.text != "assistant response: say hello" {
		t.Fatalf("extractRunResult() text = %q raw=%q", result.text, result.raw)
	}
}

func TestHelperTranscriptFixture(t *testing.T) {
	got := "assistant response: say hello\ncodex>\n"
	if !strings.Contains(got, "assistant response: say hello\ncodex>\n") {
		t.Fatalf("helper transcript fixture = %q", got)
	}
}

func TestNormalizeRunSegmentDropsEchoedPrompt(t *testing.T) {
	text := strings.Join([]string{
		"say hello",
		"assistant response: say hello",
		"codex>",
	}, "\n")
	got := normalizeRunSegment(strings.TrimSuffix(text, "\ncodex>"), "say hello")
	if got != "assistant response: say hello" {
		t.Fatalf("normalizeRunSegment() = %q", got)
	}
}

func TestExtractRunResultPreservesPromptLikeOutput(t *testing.T) {
	text := strings.Join([]string{
		"ordinary output: codex> appears inside text",
		"assistant response: check prompt-like text",
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, "codex>", "check prompt-like text")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v text=%q raw=%q", completed, ambiguous, result.text, result.raw)
	}
	if !strings.Contains(result.text, "ordinary output: codex> appears inside text") {
		t.Fatalf("extractRunResult() text = %q", result.text)
	}
	if !strings.Contains(result.text, "assistant response: check prompt-like text") {
		t.Fatalf("extractRunResult() text = %q", result.text)
	}
}

func TestExtractRunResultPreservesMarkerLikeSubstringInBody(t *testing.T) {
	text := strings.Join([]string{
		"echoed marker substring __MYCLAW_END_1__ in text",
		"assistant response: marker echo",
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, "codex>", "marker echo")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v text=%q raw=%q", completed, ambiguous, result.text, result.raw)
	}
	if !strings.Contains(result.text, "echoed marker substring __MYCLAW_END_1__ in text") {
		t.Fatalf("extractRunResult() text = %q", result.text)
	}
	if !strings.Contains(result.text, "assistant response: marker echo") {
		t.Fatalf("extractRunResult() text = %q", result.text)
	}
}

func TestNormalizeRunSegmentDropsLeadingPrompt(t *testing.T) {
	got := normalizeRunSegment("codex>\nassistant response: say hello\n", "")
	if got != "assistant response: say hello" {
		t.Fatalf("normalizeRunSegment() = %q", got)
	}
}

func TestCleanupRunTextRemovesLeadingUserEcho(t *testing.T) {
	got := cleanupRunText("user input: say hello\nassistant response: say hello\n")
	if got != "assistant response: say hello" {
		t.Fatalf("cleanupRunText() = %q", got)
	}
}

func TestCleanupRunTextRemovesNullBytes(t *testing.T) {
	got := cleanupRunText("assistant\x00 response\n")
	if got != "assistant response" {
		t.Fatalf("cleanupRunText() = %q", got)
	}
}

func TestTimeoutRunClosesRuntime(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	runtime := &PTYRuntime{cmd: cmd, notifyCh: make(chan struct{}, 1), waitFunc: cmd.Wait}
	err := timeoutRun(runtime, context.DeadlineExceeded)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeoutRun() error = %v", err)
	}
	if runtime.cmd != nil {
		t.Fatal("timeoutRun() did not clear cmd")
	}
	process, findErr := os.FindProcess(cmd.Process.Pid)
	if findErr != nil {
		t.Fatalf("FindProcess(%d) error = %v", cmd.Process.Pid, findErr)
	}
	if signalErr := process.Signal(syscall.Signal(0)); signalErr == nil {
		t.Fatalf("process %d still alive after timeout cleanup", cmd.Process.Pid)
	}
}

func TestTimeoutRunBoundsNonCooperativeWait(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var waitCalls atomic.Int32
	runtime := &PTYRuntime{
		cmd:      cmd,
		notifyCh: make(chan struct{}, 1),
		waitFunc: func() error {
			waitCalls.Add(1)
			select {}
		},
	}

	start := time.Now()
	err := timeoutRun(runtime, context.DeadlineExceeded)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeoutRun() error = %v", err)
	}
	if waitCalls.Load() != 1 {
		t.Fatalf("waitFunc() calls = %d, want 1", waitCalls.Load())
	}
	if elapsed >= terminateWaitTimeout+500*time.Millisecond {
		t.Fatalf("timeoutRun() took %v, want bounded teardown under %v", elapsed, terminateWaitTimeout+500*time.Millisecond)
	}
}

func TestPTYDriverRegistersCodexPTY(t *testing.T) {
	driver, ok := agent.LookupDriver("codex-pty")
	if !ok {
		t.Fatal("expected codex-pty driver registration")
	}
	if driver == nil {
		t.Fatal("expected non-nil driver")
	}
}

func TestPTYDriverInitRejectsEmptyCommand(t *testing.T) {
	driver := NewPTYDriver()
	_, err := driver.Init(context.Background(), agent.Spec{Type: "codex-pty"})
	if err == nil {
		t.Fatal("expected empty command error")
	}
}

func TestPTYDriverInitStartsReadyRuntime(t *testing.T) {
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "ready-only"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime, ok := runtime.(*PTYRuntime)
	if !ok {
		t.Fatalf("Init() runtime type = %T, want *PTYRuntime", runtime)
	}
	defer func() {
		if err := ptyRuntime.close(); err != nil {
			t.Fatalf("close() error = %v", err)
		}
	}()
	if ptyRuntime.state != stateStarting {
		t.Fatalf("runtime state = %s, want starting", ptyRuntime.state)
	}
}

func TestPTYDriverInitDoesNotWaitForReady(t *testing.T) {
	pidFile := helperPIDFile(t, "silent-block")

	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "silent-block"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"GO_HELPER_PID_FILE":     pidFile,
		},
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime := runtime.(*PTYRuntime)
	defer closeRuntime(t, ptyRuntime)
	if ptyRuntime.state != stateStarting {
		t.Fatalf("runtime state = %s, want starting", ptyRuntime.state)
	}

	pid := waitForHelperPID(t, pidFile)
	process, findErr := os.FindProcess(pid)
	if findErr != nil {
		t.Fatalf("FindProcess(%d) error = %v", pid, findErr)
	}
	if signalErr := process.Signal(syscall.Signal(0)); signalErr != nil {
		t.Fatalf("helper process %d not alive after Init success: %v", pid, signalErr)
	}
}

func TestPTYDriverInitFailsWhenChildExitsBeforePrompt(t *testing.T) {
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "exit-before-prompt"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime := runtime.(*PTYRuntime)
	defer closeRuntime(t, ptyRuntime)

	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		ptyRuntime.mu.Lock()
		readErr := ptyRuntime.readErr
		state := ptyRuntime.state
		ptyRuntime.mu.Unlock()
		if errors.Is(readErr, io.EOF) && state == stateBroken {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected runtime to break with EOF after child exited")
}

func TestPTYRuntimeRunSuccessfulSingleRequest(t *testing.T) {
	runtime := newHelperRuntime(t, "run-success")
	defer closeRuntime(t, runtime)

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "say hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "assistant response: say hello" {
		t.Fatalf("Run() text = %q raw=%q", resp.Text, resp.RawOutput)
	}
	if !strings.Contains(resp.RawOutput, "assistant response: say hello") {
		t.Fatalf("Run() raw output = %q", resp.RawOutput)
	}
	if runtime.state != stateReady {
		t.Fatalf("runtime state = %s, want ready", runtime.state)
	}
}

func TestPTYRuntimeRunTransitionsStartingRuntimeToReady(t *testing.T) {
	driver := NewPTYDriver()
	sessionRuntime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "run-success"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	runtime := sessionRuntime.(*PTYRuntime)
	defer closeRuntime(t, runtime)

	if runtime.state != stateStarting {
		t.Fatalf("runtime state = %s, want starting before first run", runtime.state)
	}

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "say hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if resp.Text != "assistant response: say hello" {
		t.Fatalf("Run() text = %q raw=%q", resp.Text, resp.RawOutput)
	}
	if runtime.state != stateReady {
		t.Fatalf("runtime state = %s, want ready after run", runtime.state)
	}
}

func TestPTYRuntimeRunIgnoresPromptLikeOutput(t *testing.T) {
	runtime := newHelperRuntime(t, "run-prompt-like-output")
	defer closeRuntime(t, runtime)

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "check prompt-like text"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(resp.Text, "assistant response: check prompt-like text") {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	if strings.Count(resp.Text, "codex>") != 1 {
		t.Fatalf("Run() text = %q, want embedded prompt-like output preserved once", resp.Text)
	}
}

func TestPTYRuntimeRunIgnoresEchoedMarkerSubstring(t *testing.T) {
	runtime := newHelperRuntime(t, "run-marker-echo")
	defer closeRuntime(t, runtime)

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "marker echo"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(resp.Text, "echoed marker substring __MYCLAW_END_1__ in text") {
		t.Fatalf("Run() text = %q", resp.Text)
	}
	if !strings.Contains(resp.Text, "assistant response: marker echo") {
		t.Fatalf("Run() text = %q", resp.Text)
	}
}

func TestPTYRuntimeRunTimeoutKillsProcess(t *testing.T) {
	pidFile := helperPIDFile(t, "run-hang")
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "run-hang"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"GO_HELPER_PID_FILE":     pidFile,
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime := runtime.(*PTYRuntime)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = ptyRuntime.Run(ctx, agent.Request{Prompt: "stall forever"})
	if err == nil || (!strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "EOF")) {
		t.Fatalf("Run() error = %v, want timeout-oriented failure", err)
	}
	if ptyRuntime.state != stateBroken {
		t.Fatalf("runtime state = %s, want broken", ptyRuntime.state)
	}

	pid := waitForHelperPID(t, pidFile)
	if _, findErr := os.FindProcess(pid); findErr != nil {
		t.Fatalf("FindProcess(%d) error = %v", pid, findErr)
	}
	waitForProcessExit(t, pid, 2*time.Second)
}

func TestPTYRuntimeRunCallerCancellationKeepsHealthyRuntime(t *testing.T) {
	runtime := newHelperRuntime(t, "run-delayed-success")
	defer closeRuntime(t, runtime)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := runtime.Run(ctx, agent.Request{Prompt: "slow success"})
	if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Run() error = %v, want caller cancellation", err)
	}
	if runtime.state != stateRunning {
		t.Fatalf("runtime state = %s, want running while canceled run drains", runtime.state)
	}

	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		runtime.mu.Lock()
		state := runtime.state
		runtime.mu.Unlock()
		if state == stateReady {
			goto ready
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected runtime to return to ready after canceled run drained")

ready:
	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "after cancel"})
	if err != nil {
		t.Fatalf("Run() after caller cancel error = %v", err)
	}
	if resp.Text != "assistant response: after cancel" {
		t.Fatalf("Run() after caller cancel text = %q raw=%q", resp.Text, resp.RawOutput)
	}
	if strings.Contains(resp.RawOutput, "slow success") {
		t.Fatalf("Run() after caller cancel raw=%q, want prior canceled output removed", resp.RawOutput)
	}
}

func TestPTYRuntimeRunCallerDeadlineBreaksRuntime(t *testing.T) {
	runtime := newHelperRuntime(t, "run-delayed-success")
	defer closeRuntime(t, runtime)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := runtime.Run(ctx, agent.Request{Prompt: "slow success"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %v, want caller deadline timeout", err)
	}
	if runtime.state != stateBroken {
		t.Fatalf("runtime state = %s, want broken", runtime.state)
	}
}

func TestPTYRuntimeRunCallerDeadlineTearsDownRuntime(t *testing.T) {
	pidFile := helperPIDFile(t, "run-deadline-timeout")
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "run-hang"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"GO_HELPER_PID_FILE":     pidFile,
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime := runtime.(*PTYRuntime)
	defer closeRuntime(t, ptyRuntime)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = ptyRuntime.Run(ctx, agent.Request{Prompt: "stall forever"})
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, io.EOF)) || (!strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "EOF")) {
		t.Fatalf("Run() error = %v, want caller deadline timeout or EOF teardown race", err)
	}
	if ptyRuntime.state != stateBroken {
		t.Fatalf("runtime state = %s, want broken", ptyRuntime.state)
	}

	pid := waitForHelperPID(t, pidFile)
	waitForProcessExit(t, pid, 2*time.Second)
}

func TestPTYRuntimeRunEOFBeforeCompletionBreaksRuntime(t *testing.T) {
	runtime := newHelperRuntime(t, "run-eof")
	defer closeRuntime(t, runtime)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := runtime.Run(ctx, agent.Request{Prompt: "exit now"})
	if !errors.Is(err, io.EOF) && (err == nil || !strings.Contains(err.Error(), "EOF")) {
		t.Fatalf("Run() error = %v, want EOF", err)
	}
	if runtime.state != stateBroken {
		t.Fatalf("runtime state = %s, want broken", runtime.state)
	}
}

func TestPTYRuntimeRunEOFCanMaskHardTimeoutAsTerminalEOF(t *testing.T) {
	pidFile := helperPIDFile(t, "run-hang-default-timeout")
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "run-hang"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"GO_HELPER_PID_FILE":     pidFile,
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime := runtime.(*PTYRuntime)
	defer closeRuntime(t, ptyRuntime)

	originalTimeout := defaultRunTimeout
	defaultRunTimeout = 50 * time.Millisecond
	defer func() { defaultRunTimeout = originalTimeout }()

	_, err = ptyRuntime.Run(context.Background(), agent.Request{Prompt: "stall forever"})
	if err == nil || (!strings.Contains(err.Error(), "timed out") && !errors.Is(err, io.EOF)) {
		t.Fatalf("Run() error = %v, want hard-timeout or EOF teardown failure", err)
	}
	if ptyRuntime.state != stateBroken {
		t.Fatalf("runtime state = %s, want broken", ptyRuntime.state)
	}

	pid := waitForHelperPID(t, pidFile)
	waitForProcessExit(t, pid, 2*time.Second)
}

func TestPTYRuntimeRunTimeoutMarksBroken(t *testing.T) {
	runtime := newHelperRuntime(t, "run-hang")
	defer closeRuntime(t, runtime)

	_, err := runtime.Run(context.Background(), agent.Request{Prompt: "stall forever"})
	if err == nil || (!strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "EOF")) {
		t.Fatalf("Run() error = %v, want timeout or EOF broken failure", err)
	}
	if runtime.state != stateBroken {
		t.Fatalf("runtime state = %s, want broken", runtime.state)
	}

	_, err = runtime.Run(context.Background(), agent.Request{Prompt: "after timeout"})
	if err == nil || (!strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "broken")) {
		t.Fatalf("Run() after timeout error = %v, want broken error", err)
	}
}

func TestClassifyContextTermination(t *testing.T) {
	callerCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := classifyContextTermination(nil, callerCtx, context.Canceled); err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("classifyContextTermination() canceled = %v", err)
	}
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), time.Hour)
	deadlineCancel()
	if err := classifyContextTermination(nil, deadlineCtx, context.DeadlineExceeded); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("classifyContextTermination() deadline = %v", err)
	}
}

func TestShouldBreakRuntime(t *testing.T) {
	if shouldBreakRuntime(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Fatal("shouldBreakRuntime() canceled = true, want false")
	}
	if shouldBreakRuntime(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)) {
		t.Fatal("shouldBreakRuntime() deadline = true, want false")
	}
	if !shouldBreakRuntime(io.EOF) {
		t.Fatal("shouldBreakRuntime() EOF = false, want true")
	}
}

func TestPTYRuntimeRunIsSerialAcrossConcurrentCalls(t *testing.T) {
	runtime := newHelperRuntime(t, "run-serial")
	defer closeRuntime(t, runtime)

	var wg sync.WaitGroup
	results := make([]agent.Response, 2)
	errs := make([]error, 2)
	prompts := []string{"first", "second"}

	for i := range prompts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = runtime.Run(context.Background(), agent.Request{Prompt: prompts[i]})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Run(%d) error = %v", i, err)
		}
	}
	got := []string{results[0].Text, results[1].Text}
	want := map[string]bool{
		"assistant response: first":  true,
		"assistant response: second": true,
	}
	for i, text := range got {
		if !want[text] {
			t.Fatalf("response %d = %q", i, text)
		}
		delete(want, text)
	}
	if len(want) != 0 {
		t.Fatalf("missing responses: %#v", want)
	}
}

func TestPTYRuntimeCloseWhileReaderShutsDownIsSafe(t *testing.T) {
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "ready-then-block"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ptyRuntime, ok := runtime.(*PTYRuntime)
	if !ok {
		t.Fatalf("Init() runtime type = %T, want *PTYRuntime", runtime)
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- ptyRuntime.close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close() timed out")
	}
}

func TestPTYRuntimeCloseReapsChildWhenProcessExitsDuringCleanup(t *testing.T) {
	pidFile := helperPIDFile(t, "self-exit")

	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "self-exit"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"GO_HELPER_PID_FILE":     pidFile,
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ptyRuntime, ok := runtime.(*PTYRuntime)
	if !ok {
		t.Fatalf("Init() runtime type = %T, want *PTYRuntime", runtime)
	}

	pid := waitForHelperPID(t, pidFile)
	waitForProcessExit(t, pid, 2*time.Second)

	var wg sync.WaitGroup
	wg.Add(2)

	closeErrs := make(chan error, 2)
	for range 2 {
		go func() {
			defer wg.Done()
			closeErrs <- ptyRuntime.close()
		}()
	}

	wg.Wait()
	close(closeErrs)
	for err := range closeErrs {
		if err != nil {
			t.Fatalf("close() error = %v", err)
		}
	}

	process, findErr := os.FindProcess(pid)
	if findErr != nil {
		t.Fatalf("FindProcess(%d) error = %v", pid, findErr)
	}
	if signalErr := process.Signal(syscall.Signal(0)); signalErr == nil {
		t.Fatalf("helper process %d still alive after close", pid)
	}
}

func TestPTYRuntimeCloseBoundsNonCooperativeWait(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var waitCalls atomic.Int32
	runtime := &PTYRuntime{
		cmd:      cmd,
		notifyCh: make(chan struct{}, 1),
		waitFunc: func() error {
			waitCalls.Add(1)
			select {}
		},
	}

	start := time.Now()
	err := runtime.close()
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "timed out waiting for process") {
		t.Fatalf("close() error = %v, want timeout", err)
	}
	if waitCalls.Load() != 1 {
		t.Fatalf("waitFunc() calls = %d, want 1", waitCalls.Load())
	}
	if elapsed >= terminateWaitTimeout+500*time.Millisecond {
		t.Fatalf("close() took %v, want bounded teardown under %v", elapsed, terminateWaitTimeout+500*time.Millisecond)
	}
}

func newHelperRuntime(t *testing.T, mode string) *PTYRuntime {
	t.Helper()
	driver := NewPTYDriver()
	runtime, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", mode},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ptyRuntime, ok := runtime.(*PTYRuntime)
	if !ok {
		t.Fatalf("Init() runtime type = %T, want *PTYRuntime", runtime)
	}
	return ptyRuntime
}

func closeRuntime(t *testing.T, runtime *PTYRuntime) {
	t.Helper()
	if err := runtime.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
}

func waitForHelperPID(t *testing.T, pidFile string) int {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pidBytes, err := os.ReadFile(pidFile)
		if err == nil {
			pid, scanErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if scanErr != nil {
				t.Fatalf("Atoi(pid) error = %v", scanErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for pid file %q", pidFile)
	return 0
}

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid))
		out, err := cmd.Output()
		if err != nil {
			return
		}
		if strings.Contains(strings.TrimSpace(string(out)), "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for helper process %d to exit", pid)
}

func TestHelperProcessCodexPTY(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "ready-only":
		fmt.Println("codex>")
		blockForever()
	case "silent-block":
		if pidFile := os.Getenv("GO_HELPER_PID_FILE"); pidFile != "" {
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				os.Exit(3)
			}
		}
		blockForever()
	case "self-exit":
		if pidFile := os.Getenv("GO_HELPER_PID_FILE"); pidFile != "" {
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				os.Exit(3)
			}
		}
		fmt.Println("codex>")
		time.Sleep(50 * time.Millisecond)
		os.Exit(0)
	case "exit-before-prompt":
		fmt.Print("starting up\n")
		os.Exit(0)
	case "ready-then-block":
		fmt.Println("codex>")
		blockForever()
	case "run-success":
		helperRunLoop(func(prompt string) helperRunAction {
			return helperRunAction{output: "assistant response: " + prompt + "\n"}
		})
	case "run-hang":
		if pidFile := os.Getenv("GO_HELPER_PID_FILE"); pidFile != "" {
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				os.Exit(3)
			}
		}
		fmt.Println("codex>")
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				select {}
			}
			input := string(buf[:n])
			if strings.Contains(input, "stall forever") {
				fmt.Print("working...\n")
				blockForever()
			}
		}
	case "run-eof":
		helperRunLoop(func(prompt string) helperRunAction {
			_ = prompt
			return helperRunAction{output: "partial response\n", exitAfter: true}
		})
	case "run-serial":
		helperRunLoop(func(prompt string) helperRunAction {
			time.Sleep(50 * time.Millisecond)
			return helperRunAction{output: "assistant response: " + prompt + "\n"}
		})
	case "run-delayed-success":
		helperRunLoop(func(prompt string) helperRunAction {
			time.Sleep(100 * time.Millisecond)
			return helperRunAction{output: "assistant response: " + prompt + "\n"}
		})
	case "run-prompt-like-output":
		helperRunLoop(func(prompt string) helperRunAction {
			return helperRunAction{output: "ordinary output: codex> appears inside text\nassistant response: " + prompt + "\n"}
		})
	case "run-marker-echo":
		helperRunLoop(func(prompt string) helperRunAction {
			return helperRunAction{output: "echoed marker substring __MYCLAW_END_1__ in text\nassistant response: " + prompt + "\n"}
		})
	default:
		os.Exit(2)
	}
}

type helperRunAction struct {
	output    string
	holdOpen  bool
	exitAfter bool
}

func helperRunLoop(handle func(string) helperRunAction) {
	fmt.Println("codex>")

	buffer := ""
	for {
		chunk := make([]byte, 256)
		n, err := os.Stdin.Read(chunk)
		if err != nil {
			return
		}
		buffer += normalizeOutput(string(chunk[:n]))
		for {
			idx := strings.IndexByte(buffer, '\n')
			if idx < 0 {
				break
			}
			line := strings.TrimSpace(buffer[:idx])
			buffer = buffer[idx+1:]
			if line == "" {
				continue
			}
			promptText := line
			action := handle(promptText)
			if action.exitAfter {
				if action.output != "" {
					fmt.Print(action.output)
				}
				os.Exit(0)
			}
			fmt.Print(helperTranscriptShape(promptText, action.output))
			if action.holdOpen {
				blockForever()
			}
		}
	}
}

func blockForever() {
	for {
		time.Sleep(time.Second)
	}
}

func helperPIDFile(t *testing.T, mode string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), mode+".pid")
}
