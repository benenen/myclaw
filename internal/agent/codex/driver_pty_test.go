package codex

import (
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

func TestStripANSI(t *testing.T) {
	got := stripANSI("\x1b[31mhello\x1b[0m\r\n")
	if got != "hello\n" {
		t.Fatalf("stripANSI() = %q", got)
	}
}

func TestStripANSI_OSCSequence(t *testing.T) {
	got := stripANSI("prefix\x1b]0;window title\x07suffix")
	if got != "prefixsuffix" {
		t.Fatalf("stripANSI() = %q", got)
	}
}

func TestStripANSI_StringControlSequences(t *testing.T) {
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
			got := stripANSI(tt.input)
			if got != "prefixsuffix" {
				t.Fatalf("stripANSI() = %q", got)
			}
		})
	}
}

func TestStripANSI_SingleCharacterEscapeSequences(t *testing.T) {
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
			got := stripANSI(tt.input)
			if got != "prefixsuffix" {
				t.Fatalf("stripANSI() = %q", got)
			}
		})
	}
}

func TestStripANSI_TwoByteCharsetEscapeSequences(t *testing.T) {
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
			got := stripANSI(tt.input)
			if got != "prefixsuffix" {
				t.Fatalf("stripANSI() = %q", got)
			}
		})
	}
}

func TestNormalizeOutput(t *testing.T) {
	got := normalizeOutput("a\r\nb\rc")
	if got != "a\nb\nc" {
		t.Fatalf("normalizeOutput() = %q", got)
	}
}

func TestFindMarkerRequiresOwnLine(t *testing.T) {
	idx := findMarker("before __MYCLAW_END_123__ after", "__MYCLAW_END_123__")
	if idx >= 0 {
		t.Fatalf("findMarker() = %d, want no match for inline marker", idx)
	}

	idx = findMarker("before\n__MYCLAW_END_123__\nafter", "__MYCLAW_END_123__")
	if idx < 0 {
		t.Fatal("expected marker index for line-delimited marker")
	}
}

func TestHasPromptRequiresDedicatedPromptLine(t *testing.T) {
	if prompt, ok := hasPrompt("status > running\nnext line\n"); ok {
		t.Fatalf("hasPrompt() = %q, true; want no prompt", prompt)
	}
	if prompt, ok := hasPrompt("prefix codex> suffix\n"); ok {
		t.Fatalf("hasPrompt() = %q, true; want no prompt", prompt)
	}
	if prompt, ok := hasPrompt("booting\ncodex>\n"); !ok || prompt != "codex>" {
		t.Fatalf("hasPrompt() = %q, %v; want codex prompt", prompt, ok)
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

func TestExtractRunResultIgnoresInlineMarkerSubstring(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"assistant mentions __MYCLAW_END_1__ inline",
		marker,
		"codex>",
	}, "\n")

	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", "")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v", completed, ambiguous)
	}
	if result.text != "assistant mentions __MYCLAW_END_1__ inline" {
		t.Fatalf("extractRunResult() text = %q", result.text)
	}
}

func TestExtractRunResultWaitsForPromptAfterMarker(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"answer",
		marker,
		"status codex> still output",
	}, "\n")

	completed, _, ambiguous := extractRunResult(text, 0, marker, "codex>", "")
	if completed || ambiguous {
		t.Fatalf("extractRunResult() = completed %v ambiguous %v, want incomplete", completed, ambiguous)
	}
}

func TestBuildRunPayloadMatchesHelperProtocol(t *testing.T) {
	payload := buildRunPayload("say hello", "__MYCLAW_END_1__")
	want := "__MYCLAW_END_1__\nsay hello\n__MYCLAW_END_1__\n"
	if payload != want {
		t.Fatalf("buildRunPayload() = %q, want %q", payload, want)
	}
}

func TestExtractRunResultMatchesHelperProtocol(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"say hello",
		marker,
		marker,
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", "say hello")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v text=%q raw=%q", completed, ambiguous, result.text, result.raw)
	}
	if result.text != "assistant response: say hello" {
		t.Fatalf("extractRunResult() text = %q raw=%q", result.text, result.raw)
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

func TestHelperProtocolFixture(t *testing.T) {
	got := strings.Join([]string{
		"codex>",
		"__MYCLAW_END_1__",
		"say hello",
		"__MYCLAW_END_1__",
		"assistant response: say hello",
		"__MYCLAW_END_1__",
		"codex>",
	}, "\n") + "\n"
	if !strings.Contains(got, "__MYCLAW_END_1__\nsay hello\n__MYCLAW_END_1__\nassistant response: say hello\n__MYCLAW_END_1__\ncodex>\n") {
		t.Fatalf("helper protocol fixture = %q", got)
	}
}

func TestOutputEndBoundaryMatchesPromptLine(t *testing.T) {
	text := "assistant response: say hello\ncodex>\n"
	idx, ok := outputEndBoundary(text, "codex>", 0)
	if !ok || idx != len("assistant response: say hello\n") {
		t.Fatalf("outputEndBoundary() idx=%d ok=%v", idx, ok)
	}
}

func TestClosingMarkerBoundaryFindsFirstMarkerAfterPayload(t *testing.T) {
	text := strings.Join([]string{
		"__MYCLAW_END_1__",
		"say hello",
		"__MYCLAW_END_1__",
		"assistant response: say hello",
		"__MYCLAW_END_1__",
		"codex>",
	}, "\n")
	_, payloadEnd, ok := payloadBoundary(text, 0, "__MYCLAW_END_1__")
	if !ok {
		t.Fatal("payloadBoundary() failed")
	}
	idx, _, ok := closingMarkerBoundary(text, "__MYCLAW_END_1__", payloadEnd)
	if !ok || idx != len("__MYCLAW_END_1__\nsay hello\n") {
		t.Fatalf("closingMarkerBoundary() idx=%d ok=%v", idx, ok)
	}
}

func TestClosingMarkerBoundaryAfterEchoFindsFinalMarker(t *testing.T) {
	text := strings.Join([]string{
		"assistant response: say hello",
		"__MYCLAW_END_1__",
		"codex>",
	}, "\n")
	idx, _, ok := closingMarkerBoundary(text, "__MYCLAW_END_1__", 0)
	if !ok || text[idx:] == "" || !strings.HasPrefix(text[idx:], "__MYCLAW_END_1__\ncodex>") {
		t.Fatalf("closingMarkerBoundary() idx=%d ok=%v tail=%q", idx, ok, text[idx:])
	}
}

func TestHelperProtocolBodyStartsAfterEchoMarker(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"say hello",
		marker,
		"assistant response: say hello",
		marker,
		"codex>",
	}, "\n")
	_, payloadEnd, ok := payloadBoundary(text, 0, marker)
	if !ok {
		t.Fatal("payloadBoundary() failed")
	}
	idx, _, ok := closingMarkerBoundary(text, marker, payloadEnd)
	if !ok {
		t.Fatal("closingMarkerBoundary() failed")
	}
	if body := text[markerLineEnd(text, idx, marker):]; !strings.HasPrefix(body, "assistant response: say hello\n__MYCLAW_END_1__\ncodex>") {
		t.Fatalf("body = %q", body)
	}
}

func TestSliceRunOutputWithHelperProtocolSegment(t *testing.T) {
	text := strings.Join([]string{"prefix", "assistant response: say hello", "__MYCLAW_END_1__", "codex>"}, "\n")
	out, err := sliceRunOutput(text, len("prefix\n"), "__MYCLAW_END_1__")
	if err != nil || out != "assistant response: say hello\n" {
		t.Fatalf("sliceRunOutput() out=%q err=%v", out, err)
	}
}

func TestHelperProtocolBodySliceStartsAfterEchoMarker(t *testing.T) {
	text := strings.Join([]string{"__MYCLAW_END_1__", "say hello", "__MYCLAW_END_1__", "assistant response: say hello", "__MYCLAW_END_1__", "codex>"}, "\n")
	_, payloadEnd, ok := payloadBoundary(text, 0, "__MYCLAW_END_1__")
	if !ok {
		t.Fatal("payloadBoundary() failed")
	}
	idx, _, ok := closingMarkerBoundary(text, "__MYCLAW_END_1__", payloadEnd)
	if !ok {
		t.Fatal("closingMarkerBoundary() failed")
	}
	out, err := sliceRunOutput(text, markerLineEnd(text, idx, "__MYCLAW_END_1__"), "__MYCLAW_END_1__")
	if err != nil || out != "assistant response: say hello\n" {
		t.Fatalf("sliceRunOutput() out=%q err=%v", out, err)
	}
}

func TestExtractRunResultMatchesHelperProtocolWithPromptEcho(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"say hello",
		marker,
		"say hello",
		marker,
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", "say hello")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v text=%q raw=%q", completed, ambiguous, result.text, result.raw)
	}
	if result.text != "say hello" {
		t.Fatalf("extractRunResult() text = %q raw=%q", result.text, result.raw)
	}
}

func TestExtractRunResultMatchesHelperProtocolWithAssistantBody(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"say hello",
		marker,
		"assistant response: say hello",
		marker,
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", "say hello")
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v text=%q raw=%q", completed, ambiguous, result.text, result.raw)
	}
	if result.text != "assistant response: say hello" {
		t.Fatalf("extractRunResult() text = %q raw=%q", result.text, result.raw)
	}
}

func TestExtractRunResultPreservesPromptLikeOutput(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"check prompt-like text",
		marker,
		"ordinary output: codex> appears inside text",
		"assistant response: check prompt-like text",
		marker,
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", "check prompt-like text")
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

func TestExtractRunResultPreservesMarkerSubstringInBody(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	text := strings.Join([]string{
		marker,
		"marker echo",
		marker,
		"echoed marker substring __MYCLAW_END_1__ in text",
		"assistant response: marker echo",
		marker,
		"codex>",
	}, "\n")
	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", "marker echo")
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

func TestExtractRunResultStripsPromptEchoProtocolNoise(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	promptText := "say hello"
	text := strings.Join([]string{
		marker,
		promptText,
		marker,
		promptText,
		marker,
		"codex>",
	}, "\n")

	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", promptText)
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v", completed, ambiguous)
	}
	if result.text != promptText {
		t.Fatalf("extractRunResult() text = %q raw=%q", result.text, result.raw)
	}
}

func TestExtractRunResultStripsAssistantProtocolNoise(t *testing.T) {
	marker := "__MYCLAW_END_1__"
	promptText := "say hello"
	text := strings.Join([]string{
		marker,
		promptText,
		marker,
		"assistant response: " + promptText,
		marker,
		"codex>",
	}, "\n")

	completed, result, ambiguous := extractRunResult(text, 0, marker, "codex>", promptText)
	if !completed || ambiguous {
		t.Fatalf("extractRunResult() completed=%v ambiguous=%v", completed, ambiguous)
	}
	if result.text != "assistant response: say hello" {
		t.Fatalf("extractRunResult() text = %q raw=%q", result.text, result.raw)
	}
}

func TestSliceRunOutputWithHelperProtocolSegmentAfterEcho(t *testing.T) {
	text := strings.Join([]string{"__MYCLAW_END_1__", "say hello", "__MYCLAW_END_1__", "assistant response: say hello", "__MYCLAW_END_1__", "codex>"}, "\n")
	start := len("__MYCLAW_END_1__\nsay hello\n__MYCLAW_END_1__\n")
	out, err := sliceRunOutput(text, start, "__MYCLAW_END_1__")
	if err != nil || out != "assistant response: say hello\n" {
		t.Fatalf("sliceRunOutput() out=%q err=%v", out, err)
	}
}

func TestCleanupRunTextRemovesLeadingUserEcho(t *testing.T) {
	got := cleanupRunText("user input: say hello\nassistant response: say hello\n")
	if got != "assistant response: say hello" {
		t.Fatalf("cleanupRunText() = %q", got)
	}
}

func TestBuildRunPayloadIncludesPromptBetweenMarkers(t *testing.T) {
	payload := buildRunPayload("say hello", "__MYCLAW_END_1__")
	if !strings.Contains(payload, "__MYCLAW_END_1__\nsay hello\n__MYCLAW_END_1__") {
		t.Fatalf("buildRunPayload() = %q", payload)
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

func TestSliceRunOutput(t *testing.T) {
	out, err := sliceRunOutput("prefix\nanswer\n__MYCLAW_END_1__\ncodex> ", len("prefix\n"), "__MYCLAW_END_1__")
	if err != nil {
		t.Fatalf("sliceRunOutput() error = %v", err)
	}
	if out != "answer\n" {
		t.Fatalf("sliceRunOutput() = %q", out)
	}
}

func TestSliceRunOutputRejectsEmptyMarker(t *testing.T) {
	_, err := sliceRunOutput("output", 0, "")
	if err == nil || !strings.Contains(err.Error(), "marker must not be empty") {
		t.Fatalf("sliceRunOutput() error = %v, want empty marker error", err)
	}
}

func TestSliceRunOutputMissingMarker(t *testing.T) {
	_, err := sliceRunOutput("output", 0, "__MYCLAW_END_1__")
	if err == nil || !strings.Contains(err.Error(), "marker not found") {
		t.Fatalf("sliceRunOutput() error = %v, want missing marker error", err)
	}
}

func TestSliceRunOutputInvalidStartOffset(t *testing.T) {
	_, err := sliceRunOutput("output", -1, "__MYCLAW_END_1__")
	if err == nil || !strings.Contains(err.Error(), "invalid start offset") {
		t.Fatalf("sliceRunOutput() error = %v, want invalid start offset error", err)
	}
}

func TestSliceRunOutputMarkerAtStartOffset(t *testing.T) {
	out, err := sliceRunOutput("__MYCLAW_END_1__\ncodex> ", 0, "__MYCLAW_END_1__")
	if err != nil {
		t.Fatalf("sliceRunOutput() error = %v", err)
	}
	if out != "" {
		t.Fatalf("sliceRunOutput() = %q, want empty output", out)
	}
}

func TestSliceRunOutputMarkerAtExactIndexAfterStart(t *testing.T) {
	text := "prefix\n__MYCLAW_END_1__\ncodex> "
	out, err := sliceRunOutput(text, len("prefix\n"), "__MYCLAW_END_1__")
	if err != nil {
		t.Fatalf("sliceRunOutput() error = %v", err)
	}
	if out != "" {
		t.Fatalf("sliceRunOutput() = %q, want empty output", out)
	}
}

func TestSliceRunOutputInvalidStartOffsetPastEnd(t *testing.T) {
	_, err := sliceRunOutput("output", len("output")+1, "__MYCLAW_END_1__")
	if err == nil || !strings.Contains(err.Error(), "invalid start offset") {
		t.Fatalf("sliceRunOutput() error = %v, want invalid start offset error", err)
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
	if ptyRuntime.state != stateReady {
		t.Fatalf("runtime state = %s, want ready", ptyRuntime.state)
	}
	if ptyRuntime.prompt != "codex>" {
		t.Fatalf("runtime prompt = %q, want codex>", ptyRuntime.prompt)
	}
}

func TestPTYDriverInitCleansUpProcessOnReadyTimeout(t *testing.T) {
	pidFile := helperPIDFile(t, "silent-block")

	driver := NewPTYDriver()
	_, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "silent-block"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"GO_HELPER_PID_FILE":     pidFile,
		},
		Timeout: 100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}

	pidBytes, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("ReadFile(%q) error = %v", pidFile, readErr)
	}

	pid, scanErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if scanErr != nil {
		t.Fatalf("Atoi(pid) error = %v", scanErr)
	}

	process, findErr := os.FindProcess(pid)
	if findErr != nil {
		t.Fatalf("FindProcess(%d) error = %v", pid, findErr)
	}
	if signalErr := process.Signal(syscall.Signal(0)); signalErr == nil {
		t.Fatalf("helper process %d still alive after Init failure", pid)
	}
}

func TestPTYDriverInitFailsWhenChildExitsBeforePrompt(t *testing.T) {
	driver := NewPTYDriver()
	_, err := driver.Init(context.Background(), agent.Spec{
		Type:    "codex-pty",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessCodexPTY", "--", "exit-before-prompt"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Timeout: 2 * time.Second,
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Init() error = %v, want io.EOF", err)
	}
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
	if runtime.state != stateReady {
		t.Fatalf("runtime state = %s, want ready", runtime.state)
	}

	resp, err := runtime.Run(context.Background(), agent.Request{Prompt: "after cancel"})
	if err != nil {
		t.Fatalf("Run() after caller cancel error = %v", err)
	}
	if resp.Text != "after cancel" {
		t.Fatalf("Run() after caller cancel text = %q raw=%q", resp.Text, resp.RawOutput)
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
		select {}
	case "silent-block":
		if pidFile := os.Getenv("GO_HELPER_PID_FILE"); pidFile != "" {
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				os.Exit(3)
			}
		}
		select {}
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
		select {}
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
				select {}
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
	haveStart := false
	var promptText string
	var marker string
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
			if !haveStart {
				if strings.HasPrefix(line, "__MYCLAW_END_") {
					haveStart = true
					marker = line
				}
				continue
			}
			if promptText == "" {
				promptText = line
				continue
			}
			if line != marker {
				continue
			}

			action := handle(promptText)
			if action.exitAfter {
				if action.output != "" {
					fmt.Print(action.output)
				}
				os.Exit(0)
			}
			fmt.Print(helperTranscriptShape(promptText, marker, action.output))
			if action.holdOpen {
				select {}
			}
			haveStart = false
			promptText = ""
			marker = ""
		}
	}
}

func helperPIDFile(t *testing.T, mode string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), mode+".pid")
}
