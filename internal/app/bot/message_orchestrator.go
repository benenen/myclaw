package bot

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/domain"
)

const (
	busyReply         = "当前请求较多，请稍后再试。"
	timeoutReply      = "处理超时，请稍后重试。"
	failedReply       = "处理失败，请稍后重试。"
	ackReply          = "收到，正在处理…"
	seenMessageTTL    = 10 * time.Minute
	cleanupInterval   = time.Minute
	processingTimeout = 30 * time.Second
	replyTimeout      = 5 * time.Second
	defaultQueueSize  = 1
)

type InboundMessage struct {
	BotID       string
	MessageID   string
	From        string
	Text        string
	ReplyTarget channel.ReplyTarget
	ReceivedAt  time.Time
	Ctx         context.Context
}

type specResolver interface {
	Resolve(ctx context.Context, botID string) (agent.Spec, error)
}

type replyGateway interface {
	Reply(ctx context.Context, target channel.ReplyTarget, resp agent.Response) error
}

type executor interface {
	Send(ctx context.Context, botID string, spec agent.Spec, req agent.Request) (agent.Response, error)
	// SetPushSink registers the receiver for the bot's driver-initiated
	// responses (scheduled tasks); the sink must survive session recreation.
	SetPushSink(botID string, sink agent.PushSink)
	// StopBot closes the bot's agent session, stopping its scheduled tasks
	// and releasing the CLI process.
	StopBot(botID string)
}

// progressReporter begins a per-turn trace session; nil session = no tracing.
type progressReporter interface {
	Begin(ctx context.Context, target channel.ReplyTarget) channel.ProgressSession
}

type botWorker struct {
	queue chan InboundMessage
}

type deliveryItem struct {
	msg  InboundMessage
	spec agent.Spec
	sess channel.ProgressSession
}

type botState struct {
	worker    *botWorker
	pending   *InboundMessage
	handoff   chan deliveryItem // accept -> deliver, unbuffered
	stopCh    chan struct{}     // closed by StopBot to stop both goroutines
	active    int
	queueSize int
	// lastReplyTarget is where driver-initiated pushes (scheduled tasks) are
	// delivered: the reply target of the bot's most recent inbound message.
	lastReplyTarget channel.ReplyTarget
}

type seenMessageState struct {
	seenAt     time.Time
	inProgress bool
}

type BotMessageOrchestrator struct {
	mu                sync.Mutex
	bots              map[string]*botState
	seen              map[string]seenMessageState
	lastSeenCleanup   time.Time
	executor          executor
	replies           replyGateway
	resolver          specResolver
	sessions          domain.BotCLISessionRepository
	progress          progressReporter
	messageContext    func(context.Context) context.Context
	processingTimeout time.Duration
	replyTimeout      time.Duration
}

func NewBotMessageOrchestrator(executor executor, replies replyGateway, resolver specResolver, sessions domain.BotCLISessionRepository) *BotMessageOrchestrator {
	return &BotMessageOrchestrator{
		bots:              make(map[string]*botState),
		seen:              make(map[string]seenMessageState),
		executor:          executor,
		replies:           replies,
		resolver:          resolver,
		sessions:          sessions,
		processingTimeout: processingTimeout,
		replyTimeout:      replyTimeout,
		messageContext: func(ctx context.Context) context.Context {
			if ctx == nil {
				return context.Background()
			}
			return ctx
		},
	}
}

func (o *BotMessageOrchestrator) SetMessageContext(fn func(context.Context) context.Context) {
	if fn == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messageContext = fn
}

// SetProgressReporter wires the optional live-trace reporter. nil disables tracing.
func (o *BotMessageOrchestrator) SetProgressReporter(p progressReporter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.progress = p
}

func (o *BotMessageOrchestrator) beginProgress(ctx context.Context, target channel.ReplyTarget) channel.ProgressSession {
	o.mu.Lock()
	p := o.progress
	o.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Begin(ctx, target)
}

func (o *BotMessageOrchestrator) attachContext(ctx context.Context, msg InboundMessage) InboundMessage {
	o.mu.Lock()
	fn := o.messageContext
	o.mu.Unlock()
	msg.Ctx = fn(ctx)
	return msg
}

func (o *BotMessageOrchestrator) reply(msg InboundMessage, resp agent.Response) error {
	ctx := msg.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	target := msg.ReplyTarget
	if target.RecipientID == "" {
		target.RecipientID = msg.From
	}
	return o.replies.Reply(ctx, target, resp)
}

func (o *BotMessageOrchestrator) HandleMessage(ctx context.Context, msg InboundMessage) {
	if msg.Text == "" {
		return
	}

	msg = o.attachContext(ctx, msg)
	state, admitted, duplicate := o.admitMessage(msg)
	if duplicate {
		return
	}
	if !admitted {
		_ = o.reply(msg, agent.Response{Text: busyReply})
		return
	}
	if state == nil {
		return
	}
	o.ensureWorker(msg.BotID, state)
}

func (o *BotMessageOrchestrator) HandleEvent(ctx context.Context, ev channel.RuntimeEvent) {
	o.HandleMessage(ctx, InboundMessage{
		BotID:       ev.BotID,
		MessageID:   ev.MessageID,
		From:        ev.From,
		Text:        ev.Text,
		ReplyTarget: ev.ReplyTarget,
	})
}

func (o *BotMessageOrchestrator) ensureWorker(botID string, state *botState) {
	o.mu.Lock()
	if o.bots[botID] != state {
		// StopBot removed/replaced this state between admitMessage and here;
		// spawning goroutines now would bind them to a stopCh no future StopBot
		// can reach (leak). The pending message's seen entry was cleared by
		// StopBot, so it is treated as never received.
		o.mu.Unlock()
		return
	}
	if state.worker != nil {
		o.mu.Unlock()
		return
	}
	pending := state.pending
	state.pending = nil
	worker := &botWorker{queue: make(chan InboundMessage, defaultQueueSize)}
	state.worker = worker
	state.handoff = make(chan deliveryItem)
	state.stopCh = make(chan struct{})
	o.mu.Unlock()

	o.executor.SetPushSink(botID, func(pr agent.PushResponse) {
		o.deliverPush(botID, pr)
	})
	go o.runAccept(botID, worker, state)
	go o.runDeliver(state)
	if pending != nil {
		worker.queue <- *pending
	}
}

// deliverPush sends one driver-initiated response (scheduled-task tick) to the
// bot's most recent reply target. Pushes arriving before any inbound message
// established a target are dropped with a log line.
func (o *BotMessageOrchestrator) deliverPush(botID string, pr agent.PushResponse) {
	o.mu.Lock()
	state, ok := o.bots[botID]
	var target channel.ReplyTarget
	if ok {
		target = state.lastReplyTarget
	}
	o.mu.Unlock()
	if !ok || target.RecipientID == "" {
		log.Printf("push dropped, no reply target: bot_id=%s task_id=%s", botID, pr.TaskID)
		return
	}
	msg := InboundMessage{BotID: botID, ReplyTarget: target}
	o.replyWithTimeout(context.Background(), msg, pr.Response)
}

// rememberReplyTarget records where the bot's driver-initiated pushes should
// go: the reply target of its most recent inbound message.
func (o *BotMessageOrchestrator) rememberReplyTarget(msg InboundMessage) {
	target := msg.ReplyTarget
	if target.RecipientID == "" {
		target.RecipientID = msg.From
	}
	if target.RecipientID == "" {
		return
	}
	o.mu.Lock()
	if state, ok := o.bots[msg.BotID]; ok {
		state.lastReplyTarget = target
	}
	o.mu.Unlock()
}

func (o *BotMessageOrchestrator) resizeWorkerQueue(botID string, queueSize int) {
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.bots[botID]
	if !ok {
		return
	}
	state.queueSize = queueSize
	if state.worker == nil || cap(state.worker.queue) == queueSize || len(state.worker.queue) > 0 {
		return
	}
	state.worker.queue = make(chan InboundMessage, queueSize)
}

func (o *BotMessageOrchestrator) queueMessage(botID string, msg InboundMessage) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.bots[botID]
	if !ok || state.worker == nil {
		return false
	}
	select {
	case state.worker.queue <- msg:
		return true
	default:
		return false
	}
}

// runAccept drains admitted messages, resolves + acks each, and hands it to the
// deliver goroutine. It never blocks on Send. It exits when StopBot closes stopCh.
func (o *BotMessageOrchestrator) runAccept(botID string, worker *botWorker, state *botState) {
	for {
		select {
		case <-state.stopCh:
			return
		case msg := <-worker.queue:
			item, ok := o.accept(botID, msg)
			if !ok {
				continue
			}
			select {
			case state.handoff <- item:
			case <-state.stopCh:
				o.finalizeSessionOnStop(item.sess)
				o.finishMessage(item.msg, false)
				return
			}
		}
	}
}

// accept resolves the spec, acks orchestrator bots, marks the message started,
// and returns the item to hand to the deliver goroutine. On resolver failure it
// replies + finishes and returns ok=false.
func (o *BotMessageOrchestrator) accept(botID string, msg InboundMessage) (deliveryItem, bool) {
	o.markMessageStarted(msg)
	o.rememberReplyTarget(msg)
	resolveCtx, cancelResolve := o.processingContext(msg.Ctx, 0)
	spec, err := o.resolver.Resolve(resolveCtx, botID)
	cancelResolve()
	if err != nil {
		log.Printf("resolver failed: bot_id=%s message_id=%s error=%v", msg.BotID, msg.MessageID, err)
		o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: failedReply})
		o.finishMessage(msg, false)
		return deliveryItem{}, false
	}
	queueSize := spec.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	o.resizeWorkerQueue(botID, queueSize)

	base := context.WithoutCancel(msg.Ctx)
	sess := o.beginProgress(base, msg.ReplyTarget)
	if spec.Orchestrator {
		if sess != nil {
			if err := sess.Ack(base); err != nil {
				sess = nil
				o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})
			}
		} else {
			o.replyWithTimeout(msg.Ctx, msg, agent.Response{Text: ackReply})
		}
	}
	return deliveryItem{msg: msg, spec: spec, sess: sess}, true
}

// runDeliver serially runs each turn and pushes its result to the channel.
// It is the long-lived resp goroutine; it exits only when StopBot closes stopCh.
func (o *BotMessageOrchestrator) runDeliver(state *botState) {
	for {
		select {
		case <-state.stopCh:
			return
		case item := <-state.handoff:
			o.executeAndDeliver(state, item)
		}
	}
}

// finalizeSessionOnStop finalizes an in-flight progress session when the bot is
// being torn down by StopBot, so the session's background flush goroutine exits
// (Feishu's flushLoop leaks otherwise) and the trace card is not left stuck on
// "processing". Uses a fresh bounded context because the delivery context is
// already cancelled by the stop.
func (o *BotMessageOrchestrator) finalizeSessionOnStop(sess channel.ProgressSession) {
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), replyTimeout)
	defer cancel()
	sess.Fail(ctx, failedReply)
}

// executeAndDeliver runs one turn via the blocking executor.Send and delivers
// the outcome: reply + progress finalize + session persist + dedup finish.
func (o *BotMessageOrchestrator) executeAndDeliver(state *botState, item deliveryItem) {
	ctx, cancel := o.deliverContext(state, item.msg, item.spec.Timeout)
	defer cancel()

	req := agent.Request{
		BotID:     item.msg.BotID,
		UserID:    item.msg.From,
		MessageID: item.msg.MessageID,
		Prompt:    item.msg.Text,
	}
	if item.sess != nil {
		req.OnProgress = func(ev agent.ProgressEvent) { item.sess.Step(ctx, ev) }
	}

	resp, err := o.executor.Send(ctx, item.msg.BotID, item.spec, req)
	if err != nil {
		if o.isStopping(state) {
			o.finalizeSessionOnStop(item.sess)
			o.finishMessage(item.msg, false)
			return
		}
		log.Printf("agent send failed: bot_id=%s message_id=%s error=%v", item.msg.BotID, item.msg.MessageID, err)
		replyText := failedReply
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			replyText = timeoutReply
		} else if resp.RuntimeType != "" && strings.TrimSpace(resp.Text) != "" {
			replyText = resp.RuntimeType + ": " + strings.TrimSpace(resp.Text)
		}
		if item.sess != nil {
			item.sess.Fail(ctx, replyText)
		}
		o.replyWithTimeout(ctx, item.msg, agent.Response{Text: replyText})
		o.finishMessage(item.msg, false)
		return
	}

	if o.sessions != nil && strings.TrimSpace(resp.SessionID) != "" && resp.SessionID != item.spec.ResumeSessionID {
		if err := o.sessions.Upsert(context.WithoutCancel(ctx), domain.BotCLISession{
			BotID:     item.msg.BotID,
			CLIType:   resp.RuntimeType,
			SessionID: resp.SessionID,
			WorkDir:   item.spec.WorkDir,
		}); err != nil {
			log.Printf("cli session upsert failed: bot_id=%s cli=%s error=%v", item.msg.BotID, resp.RuntimeType, err)
		}
	}
	if item.sess != nil {
		item.sess.Done(ctx)
	}
	o.replyWithTimeout(ctx, item.msg, resp)
	o.finishMessage(item.msg, true)
}

// deliverContext builds the detached per-turn context: request values preserved,
// inbound cancellation dropped, a per-turn timeout applied, and StopBot able to
// interrupt the in-flight Send.
func (o *BotMessageOrchestrator) deliverContext(state *botState, msg InboundMessage, specTimeout time.Duration) (context.Context, context.CancelFunc) {
	o.mu.Lock()
	timeout := o.processingTimeout
	o.mu.Unlock()
	if specTimeout > timeout {
		timeout = specTimeout
	}
	base := context.Background()
	if msg.Ctx != nil {
		base = context.WithoutCancel(msg.Ctx)
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(base, timeout)
	} else {
		ctx, cancel = context.WithCancel(base)
	}
	// Link StopBot: closing stopCh cancels the in-flight turn.
	go func() {
		select {
		case <-state.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (o *BotMessageOrchestrator) isStopping(state *botState) bool {
	select {
	case <-state.stopCh:
		return true
	default:
		return false
	}
}

func (o *BotMessageOrchestrator) processingContext(parent context.Context, minTimeout time.Duration) (context.Context, context.CancelFunc) {
	o.mu.Lock()
	timeout := o.processingTimeout
	o.mu.Unlock()
	if minTimeout > timeout {
		timeout = minTimeout
	}
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func (o *BotMessageOrchestrator) replyWithTimeout(ctx context.Context, msg InboundMessage, resp agent.Response) {
	replyCtx, cancel := o.replyContext(ctx)
	defer cancel()

	replyDone := make(chan struct{}, 1)
	replyMsg := msg
	replyMsg.Ctx = replyCtx
	go func() {
		log.Printf("bot reply sending: bot_id=%s message_id=%s text=%q", msg.BotID, msg.MessageID, resp.Text)
		if err := o.reply(replyMsg, resp); err != nil {
			log.Printf("bot reply failed: bot_id=%s message_id=%s error=%v", msg.BotID, msg.MessageID, err)
		}
		replyDone <- struct{}{}
	}()

	select {
	case <-replyDone:
	case <-replyCtx.Done():
	}
}

func (o *BotMessageOrchestrator) replyContext(parent context.Context) (context.Context, context.CancelFunc) {
	o.mu.Lock()
	timeout := o.replyTimeout
	o.mu.Unlock()
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	if timeout <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, timeout)
}

func (o *BotMessageOrchestrator) isMessageInProgress(msg InboundMessage) bool {
	if msg.MessageID == "" {
		return false
	}
	key := msg.BotID + ":" + msg.MessageID
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.seen[key]
	return ok && state.inProgress
}

func (o *BotMessageOrchestrator) admitMessage(msg InboundMessage) (*botState, bool, bool) {
	now := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lastSeenCleanup.IsZero() || now.Sub(o.lastSeenCleanup) >= cleanupInterval {
		o.cleanupSeenLocked(now)
	}

	state, ok := o.bots[msg.BotID]
	if !ok {
		state = &botState{queueSize: defaultQueueSize}
		o.bots[msg.BotID] = state
	}

	if msg.MessageID != "" {
		key := msg.BotID + ":" + msg.MessageID
		if seenState, ok := o.seen[key]; ok {
			if seenState.inProgress || now.Sub(seenState.seenAt) < seenMessageTTL {
				return nil, false, true
			}
		}
	}

	if state.worker == nil {
		copyMsg := msg
		state.pending = &copyMsg
		if msg.MessageID != "" {
			key := msg.BotID + ":" + msg.MessageID
			o.seen[key] = seenMessageState{seenAt: now, inProgress: true}
		}
		return state, true, false
	}

	if msg.MessageID == "" {
		// No dedup key, so enqueue exactly once (like the keyed path below);
		// a full queue means reject.
		select {
		case state.worker.queue <- msg:
			return state, true, false
		default:
			return nil, false, false
		}
	}

	key := msg.BotID + ":" + msg.MessageID
	select {
	case state.worker.queue <- msg:
		o.seen[key] = seenMessageState{seenAt: now, inProgress: true}
		return state, true, false
	default:
		return nil, false, false
	}
}

func (o *BotMessageOrchestrator) markMessageStarted(msg InboundMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.bots[msg.BotID]
	if !ok {
		return
	}
	state.active++
}

func (o *BotMessageOrchestrator) finishMessage(msg InboundMessage, succeeded bool) bool {
	now := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()

	state, ok := o.bots[msg.BotID]
	if !ok {
		return true
	}
	if msg.MessageID == "" {
		if state.active > 0 {
			state.active--
		}
		return true
	}

	key := msg.BotID + ":" + msg.MessageID
	seenState, ok := o.seen[key]
	if !ok {
		return true
	}
	if !seenState.inProgress {
		return true
	}
	if state.active > 0 {
		state.active--
	}
	if succeeded {
		seenState.seenAt = now
		seenState.inProgress = false
		o.seen[key] = seenState
		return true
	}
	delete(o.seen, key)
	return true
}

// StopBot stops the bot's accept/deliver goroutines and clears its state. Bound
// to the bot's operational life: called when the bot disconnects/stops. Safe to
// call for an unknown bot (no-op). The in-flight Send (if any) is cancelled.
func (o *BotMessageOrchestrator) StopBot(botID string) {
	o.mu.Lock()
	state, ok := o.bots[botID]
	if !ok {
		o.mu.Unlock()
		return
	}
	if state.stopCh != nil {
		select {
		case <-state.stopCh:
		default:
			close(state.stopCh)
		}
	}
	delete(o.bots, botID)
	for key := range o.seen {
		if strings.HasPrefix(key, botID+":") {
			delete(o.seen, key)
		}
	}
	o.mu.Unlock()

	// Close the bot's agent session so scheduled tasks stop and the CLI
	// process is released. Outside o.mu: the close waits for any in-flight
	// (already cancelled) turn to unwind.
	o.executor.StopBot(botID)
}

func (o *BotMessageOrchestrator) SetProcessingTimeoutForTest(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d <= 0 {
		return
	}
	o.processingTimeout = d
}

func (o *BotMessageOrchestrator) SetReplyTimeoutForTest(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d <= 0 {
		return
	}
	o.replyTimeout = d
}

func (o *BotMessageOrchestrator) HasBotState(botID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.bots[botID]
	return ok
}

func (o *BotMessageOrchestrator) ActiveCount(botID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.bots[botID]
	if !ok {
		return 0
	}
	return state.active
}

func (o *BotMessageOrchestrator) cleanupSeenLocked(now time.Time) {
	for key, state := range o.seen {
		if !state.inProgress && now.Sub(state.seenAt) >= seenMessageTTL {
			delete(o.seen, key)
		}
	}
	o.lastSeenCleanup = now
}
