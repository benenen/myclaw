package bot

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

const (
	busyReply              = "当前请求较多，请稍后再试。"
	timeoutReply           = "处理超时，请稍后重试。"
	failedReply            = "处理失败，请稍后重试。"
	seenMessageTTL         = 10 * time.Minute
	cleanupInterval        = time.Minute
	workerIdleTimeout      = seenMessageTTL
	processingTimeout      = 300000 * time.Second
	replyTimeout           = 5 * time.Second
	finishWaitPollInterval = 10 * time.Millisecond
	defaultQueueSize       = 1
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
}

type botWorker struct {
	queue chan InboundMessage
}

type botState struct {
	worker    *botWorker
	pending   *InboundMessage
	active    int
	queueSize int
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
	messageContext    func(context.Context) context.Context
	workerIdleTime    time.Duration
	processingTimeout time.Duration
	replyTimeout      time.Duration
}

func NewBotMessageOrchestrator(executor executor, replies replyGateway, resolver specResolver) *BotMessageOrchestrator {
	return &BotMessageOrchestrator{
		bots:              make(map[string]*botState),
		seen:              make(map[string]seenMessageState),
		executor:          executor,
		replies:           replies,
		resolver:          resolver,
		workerIdleTime:    workerIdleTimeout,
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
	if state.worker != nil {
		o.mu.Unlock()
		return
	}
	pending := state.pending
	state.pending = nil
	worker := &botWorker{queue: make(chan InboundMessage, defaultQueueSize)}
	state.worker = worker
	o.mu.Unlock()

	go o.runWorker(botID, worker)
	if pending != nil {
		worker.queue <- *pending
	}
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

func (o *BotMessageOrchestrator) queueCapacity(botID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.bots[botID]
	if !ok || state.worker == nil {
		return defaultQueueSize
	}
	return cap(state.worker.queue)
}

func (o *BotMessageOrchestrator) waitForQueueCapacity(botID string, min int) {
	deadline := time.Now().Add(o.currentProcessingTimeout())
	if min <= 0 {
		min = defaultQueueSize
	}
	for {
		if o.queueCapacity(botID) >= min {
			return
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return
		}
		time.Sleep(finishWaitPollInterval)
	}
}

func (o *BotMessageOrchestrator) runWorker(botID string, worker *botWorker) {
	o.mu.Lock()
	idleFor := o.workerIdleTime
	o.mu.Unlock()
	idleTimer := time.NewTimer(idleFor)
	defer idleTimer.Stop()

	for {
		select {
		case msg := <-worker.queue:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			o.processMessage(botID, msg)
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleFor)
		case <-idleTimer.C:
			if o.reclaimWorker(botID, worker) {
				return
			}
			idleTimer.Reset(idleFor)
		}
	}
}

func (o *BotMessageOrchestrator) processMessage(botID string, msg InboundMessage) {
	o.markMessageStarted(msg)
	ctx, cancel := o.processingContext(msg.Ctx)
	defer cancel()

	spec, err := o.resolver.Resolve(ctx, botID)
	if err != nil {
		o.replyWithTimeout(ctx, msg, agent.Response{Text: failedReply})
		o.finishMessageEventually(msg, false)
		return
	}
	queueSize := spec.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	o.resizeWorkerQueue(botID, queueSize)
	o.waitForQueueCapacity(botID, queueSize)

	type sendResult struct {
		resp agent.Response
		err  error
	}

	sendDone := make(chan sendResult, 1)
	go func() {
		resp, err := o.executor.Send(ctx, msg.BotID, spec, agent.Request{
			BotID:     msg.BotID,
			UserID:    msg.From,
			MessageID: msg.MessageID,
			Prompt:    msg.Text,
		})
		sendDone <- sendResult{resp: resp, err: err}
	}()

	var result sendResult
	select {
	case result = <-sendDone:
	case <-ctx.Done():
		result.err = ctx.Err()
	}

	if result.err != nil {
		replyText := failedReply
		if errors.Is(result.err, context.DeadlineExceeded) || errors.Is(result.err, context.Canceled) {
			replyText = timeoutReply
		}
		o.replyWithTimeout(ctx, msg, agent.Response{Text: replyText})
		o.finishMessageEventually(msg, false)
		return
	}

	o.replyWithTimeout(ctx, msg, result.resp)
	o.finishMessageEventually(msg, true)
}

func (o *BotMessageOrchestrator) processingContext(parent context.Context) (context.Context, context.CancelFunc) {
	o.mu.Lock()
	timeout := o.processingTimeout
	o.mu.Unlock()
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
		_ = o.reply(replyMsg, resp)
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

func (o *BotMessageOrchestrator) finishMessageEventually(msg InboundMessage, succeeded bool) {
	o.finishMessage(msg, succeeded)
}

func (o *BotMessageOrchestrator) currentProcessingTimeout() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.processingTimeout
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
		queuedOnce := false
		for {
			select {
			case state.worker.queue <- msg:
				queuedOnce = true
			default:
				if queuedOnce {
					return state, true, false
				}
				return nil, false, false
			}
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

func (o *BotMessageOrchestrator) reclaimWorker(botID string, worker *botWorker) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.bots[botID]
	if !ok || state.worker != worker {
		return true
	}
	if state.active > 0 || state.pending != nil || len(worker.queue) > 0 {
		return false
	}
	delete(o.bots, botID)
	return true
}

func (o *BotMessageOrchestrator) SetWorkerIdleTimeoutForTest(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d <= 0 {
		return
	}
	o.workerIdleTime = d
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
