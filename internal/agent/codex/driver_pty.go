package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/creack/pty"
)

var (
	ansiPattern       = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	oscPattern        = regexp.MustCompile(`\x1b\].*?(?:\x07|\x1b\\)`)
	stringPattern     = regexp.MustCompile(`\x1b(?:P|_|\^|X).*?(?:\x07|\x1b\\)`)
	escSinglePattern  = regexp.MustCompile(`\x1b(?:[@-_]|[78])`)
	escCharsetPattern = regexp.MustCompile(`\x1b[()][0-9A-Za-z]`)
)

type PTYDriver struct{}

type runtimeState string

const (
	stateStarting runtimeState = "starting"
	stateReady    runtimeState = "ready"
	stateRunning  runtimeState = "running"
	stateBroken   runtimeState = "broken"
)

const (
	terminateWaitTimeout = 2 * time.Second
)

var defaultRunTimeout = 30 * time.Second

type PTYRuntime struct {
	mu         sync.Mutex
	runMu      sync.Mutex
	cmd        *exec.Cmd
	ptyFile    *os.File
	state      runtimeState
	prompt     string
	notifyCh   chan struct{}
	readErr    error
	raw        []byte
	normalized strings.Builder
	closeOnce  sync.Once
	waitFunc   func() error
	runSeq     uint64
}

func init() {
	agent.MustRegisterDriver("codex-pty", func() agent.Driver {
		return NewPTYDriver()
	})
}

func NewPTYDriver() *PTYDriver {
	return &PTYDriver{}
}

func (d *PTYDriver) Init(ctx context.Context, spec agent.Spec) (agent.SessionRuntime, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("codex pty driver requires command")
	}

	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	if env := flattenEnv(spec.Env); len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}

	file, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	runtime := &PTYRuntime{
		cmd:      cmd,
		ptyFile:  file,
		state:    stateStarting,
		notifyCh: make(chan struct{}, 1),
		waitFunc: cmd.Wait,
	}
	go runtime.readLoop()

	if err := runtime.waitReady(ctx, spec.Timeout); err != nil {
		_ = runtime.close()
		return nil, err
	}

	return runtime, nil
}

func (r *PTYRuntime) close() error {
	if r == nil {
		return nil
	}

	var firstErr error
	r.closeOnce.Do(func() {
		r.mu.Lock()
		ptyFile := r.ptyFile
		cmd := r.cmd
		r.ptyFile = nil
		r.cmd = nil
		r.mu.Unlock()

		if ptyFile != nil {
			if err := ptyFile.Close(); err != nil && !isTerminalReadError(err) && !isAlreadyClosed(err) {
				firstErr = err
			}
		}
		if cmd != nil {
			if err := terminateAndWait(cmd, r.waitFunc); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

func isAlreadyClosed(err error) bool {
	return strings.Contains(err.Error(), "file already closed")
}

func terminateAndWait(cmd *exec.Cmd, waitFn func() error) error {
	if cmd == nil {
		return nil
	}

	proc := cmd.Process
	if proc == nil {
		return nil
	}
	if waitFn == nil {
		waitFn = cmd.Wait
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- waitFn()
	}()

	killErr := proc.Kill()
	if killErr != nil && !isProcessAlreadyDone(killErr) {
		select {
		case waitErr := <-waitErrCh:
			return normalizeWaitError(waitErr, killErr)
		case <-time.After(terminateWaitTimeout):
			return killErr
		}
	}

	select {
	case waitErr := <-waitErrCh:
		return normalizeWaitError(waitErr, nil)
	case <-time.After(terminateWaitTimeout):
		return fmt.Errorf("timed out waiting for process %d to exit after kill", proc.Pid)
	}
}

func normalizeWaitError(waitErr error, fallback error) error {
	if waitErr == nil || isChildAlreadyReaped(waitErr) || isExpectedProcessExit(waitErr) {
		return nil
	}
	if fallback != nil {
		return fallback
	}
	return waitErr
}

func isProcessAlreadyDone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || strings.Contains(err.Error(), "process already finished")
}

func isChildAlreadyReaped(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == syscall.ECHILD
}

func isExpectedProcessExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func (r *PTYRuntime) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	start := time.Now()
	promptText := strings.TrimSpace(req.Prompt)
	if promptText == "" {
		return agent.Response{}, fmt.Errorf("codex pty request prompt is required")
	}

	r.mu.Lock()
	if r.state == stateBroken {
		err := r.readErr
		if err == nil {
			err = fmt.Errorf("codex pty runtime is broken")
		}
		r.mu.Unlock()
		return agent.Response{}, err
	}
	if r.state != stateReady {
		state := r.state
		r.mu.Unlock()
		return agent.Response{}, fmt.Errorf("codex pty runtime is not ready: %s", state)
	}

	r.runSeq++
	runID := r.runSeq
	marker := fmt.Sprintf("__MYCLAW_END_%d__", runID)
	beforeLen := r.normalized.Len()
	ptyFile := r.ptyFile
	prompt := r.prompt
	r.state = stateRunning
	r.mu.Unlock()

	if ptyFile == nil {
		r.markBroken(fmt.Errorf("codex pty runtime is closed"))
		return agent.Response{}, fmt.Errorf("codex pty runtime is closed")
	}

	payload := buildRunPayload(promptText, marker)
	if _, err := io.WriteString(ptyFile, payload); err != nil {
		r.markBroken(fmt.Errorf("codex pty write failed: %w", err))
		return agent.Response{}, r.currentError()
	}

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		runCtx, cancel = context.WithTimeout(ctx, defaultRunTimeout)
	}
	defer cancel()

	result, err := r.waitRunCompletion(runCtx, ctx, beforeLen, marker, prompt, promptText)
	duration := time.Since(start)
	if err != nil {
		if shouldBreakRuntime(err) {
			r.markBroken(err)
		} else {
			r.mu.Lock()
			if r.state != stateBroken {
				r.state = stateReady
			}
			r.mu.Unlock()
		}
		return agent.Response{}, err
	}

	r.mu.Lock()
	if r.state != stateBroken {
		r.state = stateReady
	}
	r.mu.Unlock()

	return agent.Response{
		Text:      strings.TrimSpace(result.text),
		ExitCode:  0,
		Duration:  duration,
		RawOutput: result.raw,
	}, nil
}

type runResult struct {
	text string
	raw  string
}

func (r *PTYRuntime) waitRunCompletion(runCtx, callerCtx context.Context, start int, marker, prompt, promptText string) (runResult, error) {
	for {
		r.mu.Lock()
		text := r.normalized.String()
		err := r.readErr
		state := r.state
		r.mu.Unlock()

		if completed, result, _ := extractRunResult(text, start, marker, prompt, promptText); completed {
			return result, nil
		}

		if timeoutErr := runCtx.Err(); timeoutErr != nil {
			return runResult{}, classifyContextTermination(r, callerCtx, timeoutErr)
		}
		if callerCtx != nil && callerCtx != runCtx {
			if callerErr := callerCtx.Err(); callerErr != nil {
				return runResult{}, classifyContextTermination(r, callerCtx, callerErr)
			}
		}

		if errors.Is(err, io.EOF) {
			return runResult{}, io.EOF
		}
		if runErr := preferRunError(runCtx, err); runErr != nil {
			return runResult{}, runErr
		}
		if stateErr := waitRunCompletionState(err, state); stateErr != nil {
			if terminalErr := preferTerminalFailure(runCtx, stateErr); terminalErr != nil {
				return runResult{}, terminalErr
			}
		}

		select {
		case <-runCtx.Done():
			return runResult{}, classifyContextTermination(r, callerCtx, runCtx.Err())
		case <-r.notifyCh:
		}
		if callerCtx != nil && callerCtx != runCtx {
			select {
			case <-callerCtx.Done():
				return runResult{}, classifyContextTermination(r, callerCtx, callerCtx.Err())
			default:
			}
		}
	}
}

func (r *PTYRuntime) readLoop() {
	buf := make([]byte, 4096)
	for {
		r.mu.Lock()
		ptyFile := r.ptyFile
		r.mu.Unlock()
		if ptyFile == nil {
			r.signal()
			return
		}

		n, err := ptyFile.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			r.mu.Lock()
			r.raw = append(r.raw, buf[:n]...)
			r.normalized.WriteString(normalizeOutput(chunk))
			r.mu.Unlock()
			r.signal()
		}
		if err != nil {
			r.mu.Lock()
			if !isTerminalReadError(err) {
				r.readErr = err
				r.state = stateBroken
			} else if r.state == stateRunning || (r.state == stateStarting && r.prompt == "") {
				r.readErr = io.EOF
				r.state = stateBroken
			}
			r.mu.Unlock()
			r.signal()
			return
		}
	}
}

func (r *PTYRuntime) waitReady(ctx context.Context, timeout time.Duration) error {
	readyCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		readyCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	for {
		r.mu.Lock()
		text := r.normalized.String()
		if prompt, ok := hasPrompt(text); ok {
			r.prompt = prompt
			r.state = stateReady
			r.mu.Unlock()
			return nil
		}
		err := r.readErr
		r.mu.Unlock()
		if err != nil {
			return err
		}

		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-r.notifyCh:
		}
	}
}

func hasPrompt(text string) (string, bool) {
	if idx, ok := promptIndexOnOwnLine(text, "codex>"); ok {
		_ = idx
		return "codex>", true
	}
	if idx, ok := promptIndexOnOwnLine(text, "codex❯"); ok {
		_ = idx
		return "codex❯", true
	}
	return "", false
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

func isTerminalReadError(err error) bool {
	return err == nil || err == io.EOF || strings.Contains(err.Error(), "input/output error")
}

func (r *PTYRuntime) signal() {
	select {
	case r.notifyCh <- struct{}{}:
	default:
	}
}

func (r *PTYRuntime) markBroken(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		err = fmt.Errorf("codex pty runtime is broken")
	}
	r.readErr = err
	r.state = stateBroken
}

func (r *PTYRuntime) currentError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return r.readErr
	}
	return fmt.Errorf("codex pty runtime is broken")
}

func stripANSI(text string) string {
	text = oscPattern.ReplaceAllString(text, "")
	text = stringPattern.ReplaceAllString(text, "")
	text = ansiPattern.ReplaceAllString(text, "")
	text = escCharsetPattern.ReplaceAllString(text, "")
	text = escSinglePattern.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return text
}

func normalizeOutput(text string) string {
	text = stripANSI(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func findMarker(text, marker string) int {
	if marker == "" {
		return -1
	}
	for _, idx := range markerLineIndexes(text, marker) {
		return idx
	}
	return -1
}

func sliceRunOutput(text string, start int, marker string) (string, error) {
	if marker == "" {
		return "", fmt.Errorf("marker must not be empty")
	}
	if start < 0 || start > len(text) {
		return "", fmt.Errorf("invalid start offset: %d", start)
	}

	idx := findMarker(text[start:], marker)
	if idx < 0 {
		return "", fmt.Errorf("marker not found")
	}

	return text[start : start+idx], nil
}

func markerLineIndexes(text, marker string) []int {
	if marker == "" {
		return nil
	}

	var indexes []int
	searchFrom := 0
	for searchFrom <= len(text) {
		idx := strings.Index(text[searchFrom:], marker)
		if idx < 0 {
			break
		}
		idx += searchFrom
		lineStart := idx == 0 || text[idx-1] == '\n'
		lineEnd := idx+len(marker) == len(text) || text[idx+len(marker)] == '\n'
		if lineStart && lineEnd {
			indexes = append(indexes, idx)
		}
		searchFrom = idx + len(marker)
	}
	return indexes
}

func nextPrompt(text, prompt string) (int, bool) {
	if prompt == "" {
		return 0, false
	}
	for i, line := range strings.Split(text, "\n") {
		_ = i
		trimmed := strings.TrimSpace(line)
		if trimmed == strings.TrimSpace(prompt) {
			idx := strings.Index(text, line)
			for idx >= 0 {
				lineStart := idx == 0 || text[idx-1] == '\n'
				lineEnd := idx+len(line) == len(text) || text[idx+len(line)] == '\n'
				if lineStart && lineEnd {
					return idx, true
				}
				next := strings.Index(text[idx+len(line):], line)
				if next < 0 {
					break
				}
				idx += len(line) + next
			}
		}
	}
	return 0, false
}

func lineStartAfterMarker(text string, markerIdx int, marker string) int {
	outputStart := markerIdx + len(marker)
	if outputStart < len(text) && text[outputStart] == '\n' {
		outputStart++
	}
	return outputStart
}

func buildRunPayload(promptText, marker string) string {
	return marker + "\n" + promptText + "\n" + marker + "\n"
}

func helperTranscriptShape(prompt, marker, response string) string {
	_ = prompt
	if response == "" {
		response = "assistant response: " + prompt
	}
	if !strings.HasSuffix(response, "\n") {
		response += "\n"
	}
	return response + marker + "\n" + "codex>\n"
}

func markerLineEnd(text string, idx int, marker string) int {
	end := idx + len(marker)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return end
}

func markerIndexAfter(text, marker string, start int) (int, bool) {
	if marker == "" || start < 0 || start > len(text) {
		return 0, false
	}
	for _, idx := range markerLineIndexes(text[start:], marker) {
		return start + idx, true
	}
	return 0, false
}

func payloadBoundary(text string, start int, marker string) (int, int, bool) {
	if start < 0 || start > len(text) {
		return 0, 0, false
	}
	firstIdx, ok := markerIndexAfter(text, marker, start)
	if !ok || firstIdx != start {
		return 0, 0, false
	}
	return firstIdx, markerLineEnd(text, firstIdx, marker), true
}

func closingMarkerBoundary(text, marker string, start int) (int, int, bool) {
	idx, ok := markerIndexAfter(text, marker, start)
	if !ok {
		return 0, 0, false
	}
	return idx, markerLineEnd(text, idx, marker), true
}

func promptBlock(text string, prompt string, start int) (int, int, bool) {
	promptLine := promptLineText(prompt)
	if promptLine == "" || start < 0 || start > len(text) {
		return 0, 0, false
	}
	if !strings.HasPrefix(text[start:], promptLine) {
		return 0, 0, false
	}
	promptEnd := start + len(promptLine)
	if promptEnd < len(text) && text[promptEnd] == '\n' {
		promptEnd++
	}
	if strings.TrimSpace(text[promptEnd:]) != "" {
		return 0, 0, false
	}
	return start, promptEnd, true
}

func promptBlockFrom(text string, prompt string, start int) (int, int, bool) {
	promptLine := promptLineText(prompt)
	if promptLine == "" || start < 0 || start > len(text) {
		return 0, 0, false
	}

	for offset := start; offset <= len(text); {
		idx, ok := promptIndexOnOwnLine(text[offset:], prompt)
		if !ok {
			return 0, 0, false
		}
		promptIdx := offset + idx
		if blockStart, blockEnd, ok := promptBlock(text, prompt, promptIdx); ok {
			return blockStart, blockEnd, true
		}
		lineEnd := promptIdx + len(promptLine)
		if lineEnd < len(text) && text[lineEnd] == '\n' {
			lineEnd++
		}
		offset = lineEnd
	}
	return 0, 0, false
}

func buildRawOutput(protocol string) string {
	return protocol
}

func extractRunResult(text string, start int, marker, prompt, promptText string) (bool, runResult, bool) {
	payloadIdx, payloadEnd, ok := payloadBoundary(text, start, marker)
	if !ok || payloadIdx != start {
		return false, runResult{}, false
	}

	promptIdx, promptEnd, ok := promptBlockFrom(text, prompt, payloadEnd)
	if !ok {
		return false, runResult{}, false
	}

	segment := text[payloadEnd:promptIdx]
	body, ok := extractProtocolBody(segment, marker, strings.TrimSpace(promptText))
	if !ok {
		return false, runResult{}, false
	}

	response := normalizeRunSegment(body, strings.TrimSpace(promptText))
	protocol := helperTranscriptShape(promptText, marker, response)
	return true, runResult{
		text: response,
		raw:  buildRawOutput(protocol[:len(protocol)-len("codex>\n")] + text[promptIdx:promptEnd]),
	}, false
}

func extractProtocolBody(segment, marker, promptText string) (string, bool) {
	if marker == "" {
		return "", false
	}
	trimmed := strings.TrimLeft(segment, "\n")
	if trimmed == "" {
		return "", false
	}

	lines := strings.Split(trimmed, "\n")
	markerIndexes := make([]int, 0, 2)
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			markerIndexes = append(markerIndexes, i)
		}
	}
	if len(markerIndexes) == 0 {
		return strings.TrimRight(trimmed, "\n"), true
	}
	if len(markerIndexes) == 1 {
		body := strings.Join(lines[:markerIndexes[0]], "\n")
		if cleanupRunText(body) == "" && promptText != "" {
			return "assistant response: " + promptText, true
		}
		return body, true
	}

	between := strings.Join(lines[:markerIndexes[0]], "\n")
	if promptText != "" && cleanupRunText(between) == promptText {
		body := strings.Join(lines[markerIndexes[0]+1:markerIndexes[1]], "\n")
		if cleanupRunText(body) == "" {
			return "assistant response: " + promptText, true
		}
		return body, true
	}
	return between, true
}

func normalizeRunSegment(text, promptText string) string {
	_ = promptText
	return cleanupRunText(text)
}

func preferRunError(runCtx context.Context, readErr error) error {
	if readErr == nil || errors.Is(readErr, io.EOF) || isTerminalReadError(readErr) {
		return nil
	}
	if err := runCtx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("codex pty read failed: %w", readErr)
}

func preferTerminalFailure(runCtx context.Context, readErr error) error {
	if readErr == nil {
		return nil
	}
	if err := runCtx.Err(); err != nil {
		return err
	}
	return readErr
}

func waitRunCompletionState(readErr error, state runtimeState) error {
	if readErr != nil {
		return readErr
	}
	if state == stateBroken {
		return nil
	}
	return nil
}

func outputEndBoundary(text, prompt string, start int) (int, bool) {
	promptLine := promptLineText(prompt)
	if promptLine == "" || start < 0 || start > len(text) {
		return 0, false
	}

	offset := start
	for offset <= len(text) {
		idx, ok := promptIndexOnOwnLine(text[offset:], prompt)
		if !ok {
			return 0, false
		}
		promptIdx := offset + idx
		end := promptIdx + len(promptLine)
		if end < len(text) && text[end] == '\n' {
			end++
		}
		remainder := text[end:]
		if strings.TrimSpace(remainder) == "" {
			return promptIdx, true
		}
		offset = end
	}
	return 0, false
}

func classifyContextTermination(r *PTYRuntime, callerCtx context.Context, err error) error {
	if errors.Is(err, context.Canceled) && callerCanceledWithoutDeadline(callerCtx) {
		return fmt.Errorf("codex pty run canceled: %w", err)
	}
	return timeoutRun(r, err)
}

func shouldBreakRuntime(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func callerCanceledWithoutDeadline(ctx context.Context) bool {
	if ctx == nil || !errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	_, hasDeadline := ctx.Deadline()
	return !hasDeadline
}

func timeoutRun(r *PTYRuntime, err error) error {
	if r != nil {
		r.mu.Lock()
		r.readErr = fmt.Errorf("codex pty run timed out: %w", err)
		r.state = stateBroken
		r.mu.Unlock()
		_ = r.close()
	}
	return fmt.Errorf("codex pty run timed out: %w", err)
}

func cleanupRunText(text string) string {
	text = strings.ReplaceAll(text, "\x00", "")
	text = strings.TrimPrefix(text, "\n")
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) >= 2 && strings.HasPrefix(lines[1], "user input: ") {
		lines = lines[1:]
	}
	if len(lines) >= 1 && strings.HasPrefix(lines[0], "user input: ") {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func promptLineText(prompt string) string {
	return strings.TrimRight(prompt, " ")
}

func promptIndexOnOwnLine(text, prompt string) (int, bool) {
	promptLine := promptLineText(prompt)
	if promptLine == "" {
		return 0, false
	}

	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		current := strings.TrimSuffix(line, "\n")
		if strings.TrimSpace(current) == promptLine {
			return offset, true
		}
		offset += len(line)
	}
	if !strings.HasSuffix(text, "\n") && offset < len(text) && strings.TrimSpace(text[offset:]) == promptLine {
		return offset, true
	}
	return 0, false
}
