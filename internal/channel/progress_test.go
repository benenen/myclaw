package channel

import (
	"context"
	"testing"

	"github.com/benenen/myclaw/internal/agent"
)

type recordingSession struct{ steps int }

func (s *recordingSession) Ack(context.Context) error                 { return nil }
func (s *recordingSession) Step(context.Context, agent.ProgressEvent) { s.steps++ }
func (s *recordingSession) Done(context.Context)                      {}
func (s *recordingSession) Fail(context.Context, string)              {}

type stubReporter struct{ sess ProgressSession }

func (r stubReporter) Begin(context.Context, ReplyTarget) ProgressSession { return r.sess }

func TestMultiProgressReporter_RoutesByChannelType(t *testing.T) {
	sess := &recordingSession{}
	m := NewMultiProgressReporter()
	m.Register("feishu", stubReporter{sess: sess})

	got := m.Begin(context.Background(), ReplyTarget{ChannelType: "feishu"})
	if got != ProgressSession(sess) {
		t.Fatalf("expected the feishu session, got %#v", got)
	}
}

func TestMultiProgressReporter_UnknownChannelReturnsNil(t *testing.T) {
	m := NewMultiProgressReporter()
	if got := m.Begin(context.Background(), ReplyTarget{ChannelType: "wechat"}); got != nil {
		t.Fatalf("expected nil for unregistered channel, got %#v", got)
	}
}
