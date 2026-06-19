package channel

import (
	"context"

	"github.com/benenen/myclaw/internal/agent"
)

// ProgressSession is one in-flight turn's live trace surface. All methods are
// best-effort: implementations must never block the caller on the network for
// long and must never let a failure propagate to the answer path.
type ProgressSession interface {
	// Ack eagerly renders the initial "processing" card and returns an error if
	// it could not be created, so the caller can fall back to a plain-text ack.
	Ack(ctx context.Context) error
	// Step records one tool event and schedules a throttled card update.
	Step(ctx context.Context, ev agent.ProgressEvent)
	// Done finalizes the card as succeeded and blocks until the final frame is
	// flushed, so the answer message lands after it.
	Done(ctx context.Context)
	// Fail finalizes the card as failed/timed-out and blocks until flushed.
	Fail(ctx context.Context, reason string)
}

// ProgressReporter begins a trace session for a reply target, or returns nil
// when tracing does not apply (wrong channel, disabled, missing creds).
type ProgressReporter interface {
	Begin(ctx context.Context, target ReplyTarget) ProgressSession
}

// MultiProgressReporter routes Begin by ReplyTarget.ChannelType, mirroring
// MultiReplyGateway. An unregistered channel yields a nil session.
type MultiProgressReporter struct {
	reporters map[string]ProgressReporter
}

func NewMultiProgressReporter() *MultiProgressReporter {
	return &MultiProgressReporter{reporters: make(map[string]ProgressReporter)}
}

func (m *MultiProgressReporter) Register(channelType string, r ProgressReporter) {
	m.reporters[channelType] = r
}

func (m *MultiProgressReporter) Begin(ctx context.Context, target ReplyTarget) ProgressSession {
	r, ok := m.reporters[target.ChannelType]
	if !ok {
		return nil
	}
	return r.Begin(ctx, target)
}

var _ ProgressReporter = (*MultiProgressReporter)(nil)
