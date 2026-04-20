package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
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
	defaultPrompt        = "codex>"
)

var defaultRunTimeout = 30 * time.Second

type PTYRuntime struct {
	mu         sync.Mutex
	runMu      sync.Mutex
	cmd        *exec.Cmd
	ptyFile    *os.File
	screen     vt10x.Terminal
	state      runtimeState
	notifyCh   chan struct{}
	readErr    error
	raw        []byte
	normalized bytes.Buffer
	sanitizer  terminalSanitizer
	closeOnce  sync.Once
	waitFunc   func() error
}

type sanitizerState int

const (
	sanitizerStateNormal sanitizerState = iota
	sanitizerStateEsc
	sanitizerStateCSI
	sanitizerStateOSC
	sanitizerStateOSCEsc
	sanitizerStateString
	sanitizerStateStringEsc
	sanitizerStateCharset
)

type terminalSanitizer struct {
	state     sanitizerState
	pendingCR bool
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
		screen:   vt10x.New(vt10x.WithSize(80, 24)),
		state:    stateStarting,
		notifyCh: make(chan struct{}, 1),
		waitFunc: cmd.Wait,
	}
	go runtime.readLoop()

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

func (r *PTYRuntime) Close() error {
	return r.close()
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
	if r.state != stateReady && r.state != stateStarting {
		state := r.state
		r.mu.Unlock()
		return agent.Response{}, fmt.Errorf("codex pty runtime is not ready: %s", state)
	}

	beforeLen := r.normalized.Len()
	beforeScreen := snapshotTerminal(r.screen)
	ptyFile := r.ptyFile
	r.state = stateRunning
	r.mu.Unlock()

	if ptyFile == nil {
		r.markBroken(fmt.Errorf("codex pty runtime is closed"))
		return agent.Response{}, fmt.Errorf("codex pty runtime is closed")
	}

	payload := promptText
	if !strings.HasSuffix(payload, "\n") {
		payload += "\n"
	}
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

	result, err := r.waitRunCompletion(runCtx, ctx, beforeLen, defaultPrompt, promptText)
	duration := time.Since(start)
	if err != nil {
		if errors.Is(err, context.Canceled) && callerCanceledWithoutDeadline(ctx) {
			go r.recoverCanceledRun(beforeLen, defaultPrompt, promptText)
		} else if shouldBreakRuntime(err) {
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
		RawOutput: r.buildRawOutput(result.raw, beforeScreen),
	}, nil
}

func (r *PTYRuntime) recoverCanceledRun(start int, prompt, promptText string) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRunTimeout)
	defer cancel()

	_, err := r.waitRunCompletion(ctx, nil, start, prompt, promptText)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == stateBroken {
		return
	}
	if err != nil {
		r.readErr = err
		r.state = stateBroken
		return
	}
	r.state = stateReady
}

type runResult struct {
	text string
	raw  string
}

func (r *PTYRuntime) waitRunCompletion(runCtx, callerCtx context.Context, start int, prompt, promptText string) (runResult, error) {
	for {
		r.mu.Lock()
		text := r.normalized.String()
		err := r.readErr
		state := r.state
		r.mu.Unlock()

		if completed, result, _ := extractRunResult(text, start, prompt, promptText); completed {
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
			chunk := buf[:n]
			r.mu.Lock()
			r.raw = append(r.raw, chunk...)
			normalized := r.sanitizer.Write(&r.normalized, chunk)
			if r.screen != nil {
				_, _ = r.screen.Write(chunk)
				log.Printf("pty read %d bytes, screen now:\n%s", n, snapshotTerminal(r.screen))
			}
			r.mu.Unlock()
			if normalized != "" && debugPTYOutputEnabled() {
				_, _ = os.Stderr.WriteString(normalized)
			}
			r.signal()
		}
		if err != nil {
			r.mu.Lock()
			if !isTerminalReadError(err) {
				r.readErr = err
				r.state = stateBroken
			} else if r.state == stateRunning || r.state == stateStarting {
				r.readErr = io.EOF
				r.state = stateBroken
			}
			r.mu.Unlock()
			r.signal()
			return
		}
	}
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

func (r *PTYRuntime) screenText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshotTerminal(r.screen)
}

func (r *PTYRuntime) buildRawOutput(transcript, beforeScreen string) string {
	screen := diffScreenSnapshot(beforeScreen, r.screenText())
	if screen == "" {
		return transcript
	}
	var out strings.Builder
	out.WriteString(transcript)
	if !strings.HasSuffix(transcript, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString("screen:\n")
	out.WriteString(screen)
	if debugPTYOutputEnabled() {
		_, _ = os.Stderr.WriteString("screen:\n" + screen + "\n")
	}
	return out.String()
}

func diffScreenSnapshot(before, after string) string {
	after = strings.TrimRight(after, " \t\r\n")
	before = strings.TrimRight(before, " \t\r\n")
	if after == "" || after == before {
		return ""
	}
	if before == "" {
		return after
	}
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")
	common := 0
	for common < len(beforeLines) && common < len(afterLines) && beforeLines[common] == afterLines[common] {
		common++
	}
	return strings.TrimRight(strings.Join(afterLines[common:], "\n"), " \t\r\n")
}

func snapshotTerminal(term vt10x.Terminal) string {
	if term == nil {
		return ""
	}
	return strings.TrimRight(term.String(), " \t\r\n")
}

func normalizeOutput(text string) string {
	var out bytes.Buffer
	var sanitizer terminalSanitizer
	return sanitizer.Write(&out, []byte(text))
}

func debugPTYOutputEnabled() bool {
	value := strings.TrimSpace(os.Getenv("MYCLAW_DEBUG_PTY_OUTPUT"))
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *terminalSanitizer) Write(dst *bytes.Buffer, chunk []byte) string {
	if len(chunk) == 0 {
		return ""
	}

	var emitted bytes.Buffer
	for _, b := range chunk {
		if s.pendingCR {
			if b == '\n' {
				dst.WriteByte('\n')
				emitted.WriteByte('\n')
				s.pendingCR = false
				continue
			}
			truncateCurrentLine(dst)
			truncateCurrentLine(&emitted)
			s.pendingCR = false
		}
		s.writeByte(dst, &emitted, b)
	}
	return emitted.String()
}

func (s *terminalSanitizer) writeByte(dst, emitted *bytes.Buffer, b byte) {
	switch s.state {
	case sanitizerStateNormal:
		switch b {
		case 0x1b:
			s.state = sanitizerStateEsc
		case '\r':
			s.pendingCR = true
		case '\n':
			dst.WriteByte('\n')
			emitted.WriteByte('\n')
		case 0x00:
		default:
			dst.WriteByte(b)
			emitted.WriteByte(b)
		}
	case sanitizerStateEsc:
		switch b {
		case '[':
			s.state = sanitizerStateCSI
		case ']':
			s.state = sanitizerStateOSC
		case 'P', '_', '^', 'X':
			s.state = sanitizerStateString
		case '(', ')':
			s.state = sanitizerStateCharset
		default:
			s.state = sanitizerStateNormal
		}
	case sanitizerStateCSI:
		if b >= 0x40 && b <= 0x7e {
			s.state = sanitizerStateNormal
		}
	case sanitizerStateOSC:
		switch b {
		case 0x07:
			s.state = sanitizerStateNormal
		case 0x1b:
			s.state = sanitizerStateOSCEsc
		}
	case sanitizerStateOSCEsc:
		if b == '\\' {
			s.state = sanitizerStateNormal
			return
		}
		s.state = sanitizerStateOSC
	case sanitizerStateString:
		switch b {
		case 0x07:
			s.state = sanitizerStateNormal
		case 0x1b:
			s.state = sanitizerStateStringEsc
		}
	case sanitizerStateStringEsc:
		if b == '\\' {
			s.state = sanitizerStateNormal
			return
		}
		s.state = sanitizerStateString
	case sanitizerStateCharset:
		s.state = sanitizerStateNormal
	}
}

func truncateCurrentLine(buf *bytes.Buffer) {
	data := buf.Bytes()
	idx := bytes.LastIndexByte(data, '\n')
	if idx < 0 {
		buf.Reset()
		return
	}
	buf.Truncate(idx + 1)
}

func helperTranscriptShape(prompt, response string) string {
	if response == "" {
		response = "assistant response: " + prompt
	}
	if !strings.HasSuffix(response, "\n") {
		response += "\n"
	}
	return response + "codex>\n"
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

func extractRunResult(text string, start int, prompt, promptText string) (bool, runResult, bool) {
	if start < 0 || start > len(text) {
		return false, runResult{}, false
	}

	promptIdx, promptEnd, ok := promptBlockFrom(text, prompt, start)
	if !ok {
		return false, runResult{}, false
	}

	response := normalizeRunSegment(text[start:promptIdx], promptText)
	if response == "" && strings.TrimSpace(promptText) != "" {
		return false, runResult{}, false
	}
	return true, runResult{
		text: response,
		raw:  buildRawOutput(text[start:promptEnd]),
	}, false
}

func normalizeRunSegment(text, promptText string) string {
	if idx, end, ok := lastPromptBoundary(text); ok {
		text = text[end:]
		if idx >= 0 {
			text = strings.TrimPrefix(text, "\n")
		}
	}
	text = cleanupRunText(text)
	trimmedPrompt := strings.TrimSpace(promptText)
	if trimmedPrompt == "" || text == "" {
		return text
	}

	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == trimmedPrompt {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
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
	for len(lines) > 0 {
		trimmed := strings.TrimSpace(lines[0])
		if trimmed != defaultPrompt && trimmed != "codex❯" {
			break
		}
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

func lastPromptBoundary(text string) (int, int, bool) {
	bestIdx := -1
	bestEnd := -1
	for _, prompt := range []string{defaultPrompt, "codex❯"} {
		promptLine := promptLineText(prompt)
		if promptLine == "" {
			continue
		}
		for offset := 0; offset <= len(text); {
			idx, ok := promptIndexOnOwnLine(text[offset:], prompt)
			if !ok {
				break
			}
			absIdx := offset + idx
			end := absIdx + len(promptLine)
			if end < len(text) && text[end] == '\n' {
				end++
			}
			bestIdx = absIdx
			bestEnd = end
			offset = end
		}
	}
	if bestIdx < 0 {
		return 0, 0, false
	}
	return bestIdx, bestEnd, true
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
