package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

const maxTraceLines = 25

// traceStep is one rendered tool line.
type traceStep struct {
	tool   string
	target string
}

// traceState is the immutable snapshot buildProgressCard renders.
type traceState struct {
	steps    []traceStep
	terminal string // "" in-progress | "done" | "fail"
	reason   string // failure reason when terminal == "fail"
	elapsed  time.Duration
}

var toolEmojis = map[string]string{
	"Bash":      "🔧",
	"Read":      "📖",
	"Edit":      "✏️",
	"Write":     "✏️",
	"Grep":      "🔍",
	"Glob":      "🔍",
	"WebFetch":  "🌐",
	"WebSearch": "🌐",
	"Task":      "🤖",
}

func toolEmoji(tool string) string {
	if e, ok := toolEmojis[tool]; ok {
		return e
	}
	return "▸"
}

func traceHeader(st traceState) string {
	switch st.terminal {
	case "done":
		return fmt.Sprintf("✅ 完成 · %d 步 · %ds", len(st.steps), int(st.elapsed.Seconds()))
	case "fail":
		if strings.TrimSpace(st.reason) != "" {
			return "⚠️ 失败：" + st.reason
		}
		return "⚠️ 失败"
	default:
		return "🤖 处理中…"
	}
}

// buildProgressCard renders the trace as a feishu interactive card. Only the
// last maxTraceLines steps are shown; overflow is summarized at the top.
func buildProgressCard(st traceState) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(traceHeader(st))
	b.WriteString("**")

	steps := st.steps
	if len(steps) > maxTraceLines {
		fmt.Fprintf(&b, "\n…(+%d 步)", len(steps)-maxTraceLines)
		steps = steps[len(steps)-maxTraceLines:]
	}
	for _, s := range steps {
		b.WriteString("\n")
		b.WriteString(toolEmoji(s.tool))
		b.WriteString(" ")
		b.WriteString(s.tool)
		if s.target != "" {
			b.WriteString("  ")
			b.WriteString(s.target)
		}
	}

	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"elements": []any{map[string]any{"tag": "markdown", "content": b.String()}},
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(encoded)
}

const (
	traceMinInterval  = 700 * time.Millisecond
	tracePatchTimeout = 5 * time.Second
)

// traceTarget is the destination for one trace card.
type traceTarget struct {
	chatID         string
	replyMessageID string // set in group chats to thread under the original
}

type traceSession struct {
	baseCtx context.Context
	api     feishuAPI
	creds   Credentials
	target  traceTarget

	minInterval time.Duration
	start       time.Time

	mu        sync.Mutex
	steps     []traceStep
	messageID string
	created   bool
	degraded  bool
	terminal  string
	reason    string
	dirty     bool
	lastPatch time.Time

	startOnce sync.Once
	wake      chan struct{}
	closed    chan struct{}
	flushed   chan struct{}
}

func newTraceSession(ctx context.Context, api feishuAPI, creds Credentials, target traceTarget, minInterval time.Duration) *traceSession {
	return &traceSession{
		baseCtx:     context.WithoutCancel(ctx),
		api:         api,
		creds:       creds,
		target:      target,
		minInterval: minInterval,
		start:       time.Now(),
		wake:        make(chan struct{}, 1),
		closed:      make(chan struct{}),
		flushed:     make(chan struct{}),
	}
}

// snapshot builds the immutable render state. Caller holds s.mu.
func (s *traceSession) snapshot() traceState {
	st := traceState{terminal: s.terminal, reason: s.reason}
	st.steps = append(st.steps, s.steps...)
	if s.terminal == "done" {
		st.elapsed = time.Since(s.start)
	}
	return st
}

// ensureCardLocked creates the card if not yet created. Caller holds s.mu; the
// method temporarily releases s.mu for the network call and re-acquires before
// returning. Returns the create error (and marks degraded) on failure.
func (s *traceSession) ensureCardLocked() error {
	if s.created || s.degraded {
		return nil
	}
	st := s.snapshot()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.baseCtx, tracePatchTimeout)
	id, err := s.api.CreateCard(ctx, s.creds, CardParams{
		ChatID:         s.target.chatID,
		ReplyMessageID: s.target.replyMessageID,
		Content:        buildProgressCard(st),
	})
	cancel()
	s.mu.Lock()
	if err != nil {
		s.degraded = true
		return err
	}
	s.messageID = id
	s.created = true
	s.lastPatch = time.Now()
	return nil
}

// flushNow renders current state and creates-or-patches the card. Best-effort.
func (s *traceSession) flushNow(final bool) {
	s.mu.Lock()
	if s.degraded {
		s.mu.Unlock()
		return
	}
	if !s.created {
		if err := s.ensureCardLocked(); err != nil {
			slog.Warn("feishu trace card create failed", "error", err)
			s.mu.Unlock()
			return
		}
		if !final {
			s.dirty = false
			s.mu.Unlock()
			return // create already rendered current state
		}
	}
	st := s.snapshot()
	s.dirty = false
	s.lastPatch = time.Now()
	id := s.messageID
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(s.baseCtx, tracePatchTimeout)
	defer cancel()
	if err := s.api.PatchCard(ctx, s.creds, id, buildProgressCard(st)); err != nil {
		slog.Warn("feishu trace card patch failed", "error", err)
	}
}

func (s *traceSession) flushLoop() {
	defer close(s.flushed)
	for {
		select {
		case <-s.closed:
			s.flushNow(true)
			return
		case <-s.wake:
			s.mu.Lock()
			wait := s.minInterval - time.Since(s.lastPatch)
			s.mu.Unlock()
			if wait > 0 {
				select {
				case <-time.After(wait):
				case <-s.closed:
					s.flushNow(true)
					return
				}
			}
			s.flushNow(false)
		}
	}
}

func (s *traceSession) ensureLoop() { s.startOnce.Do(func() { go s.flushLoop() }) }

func (s *traceSession) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Ack synchronously creates the initial card and returns an error on failure.
func (s *traceSession) Ack(ctx context.Context) error {
	s.mu.Lock()
	err := s.ensureCardLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.ensureLoop()
	return nil
}

func (s *traceSession) Step(_ context.Context, ev agent.ProgressEvent) {
	s.mu.Lock()
	s.steps = append(s.steps, traceStep{tool: ev.Tool, target: ev.Target})
	s.dirty = true
	s.mu.Unlock()
	s.ensureLoop()
	s.signal()
}

func (s *traceSession) finish(kind, reason string) {
	s.mu.Lock()
	if s.terminal != "" {
		s.mu.Unlock()
		return
	}
	s.terminal = kind
	s.reason = reason
	started := s.created || len(s.steps) > 0
	s.mu.Unlock()
	if !started {
		return // nothing ever shown (zero-tool turn, no Ack): no card
	}
	s.ensureLoop()
	close(s.closed)
	<-s.flushed
}

func (s *traceSession) Done(context.Context)                  { s.finish("done", "") }
func (s *traceSession) Fail(_ context.Context, reason string) { s.finish("fail", reason) }

var _ channel.ProgressSession = (*traceSession)(nil)

// feishuProgressReporter begins trace sessions for feishu targets when tracing
// is enabled and creds resolve.
type feishuProgressReporter struct {
	api      feishuAPI
	registry *Registry
	enabled  bool
}

// NewProgressReporter creates a feishuProgressReporter.
func NewProgressReporter(api feishuAPI, registry *Registry, enabled bool) *feishuProgressReporter {
	return &feishuProgressReporter{api: api, registry: registry, enabled: enabled}
}

func (r *feishuProgressReporter) Begin(ctx context.Context, target channel.ReplyTarget) channel.ProgressSession {
	if !r.enabled || target.ChannelType != ChannelType {
		return nil
	}
	botID := strings.TrimSpace(target.MetadataValue("bot_id"))
	creds, ok := r.registry.Lookup(botID)
	if !ok {
		return nil
	}
	chatID := strings.TrimSpace(target.MetadataValue("chat_id"))
	if chatID == "" {
		chatID = strings.TrimSpace(target.RecipientID)
	}
	tt := traceTarget{chatID: chatID}
	if target.MetadataValue("chat_type") == "group" {
		tt.replyMessageID = strings.TrimSpace(target.MetadataValue("message_id"))
	}
	return newTraceSession(ctx, r.api, creds, tt, traceMinInterval)
}

var _ channel.ProgressReporter = (*feishuProgressReporter)(nil)
