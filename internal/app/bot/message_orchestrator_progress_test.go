package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	"github.com/benenen/myclaw/internal/channel"
)

type progressCounts struct{ ack, steps, done, failed int }

type fakeProgressSession struct {
	mu     sync.Mutex
	c      progressCounts
	ackErr error
}

func (s *fakeProgressSession) Ack(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.ack++
	return s.ackErr
}
func (s *fakeProgressSession) Step(context.Context, agent.ProgressEvent) {
	s.mu.Lock()
	s.c.steps++
	s.mu.Unlock()
}
func (s *fakeProgressSession) Done(context.Context)         { s.mu.Lock(); s.c.done++; s.mu.Unlock() }
func (s *fakeProgressSession) Fail(context.Context, string) { s.mu.Lock(); s.c.failed++; s.mu.Unlock() }
func (s *fakeProgressSession) counts() progressCounts       { s.mu.Lock(); defer s.mu.Unlock(); return s.c }

type fakeProgressReporter struct {
	sess    *fakeProgressSession
	nilSess bool
}

func (r *fakeProgressReporter) Begin(context.Context, channel.ReplyTarget) channel.ProgressSession {
	if r.nilSess {
		return nil
	}
	return r.sess
}

func feishuInbound(text string) InboundMessage {
	return InboundMessage{
		BotID: "bot-1", MessageID: "m1", From: "u", Text: text,
		ReplyTarget: channel.ReplyTarget{ChannelType: "feishu", RecipientID: "c"},
	}
}

func waitReply(t *testing.T, ch <-chan agent.Response) agent.Response {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply")
		return agent.Response{}
	}
}

func TestOrchestrator_PlainBot_WiresProgressOnSuccess(t *testing.T) {
	sess := &fakeProgressSession{}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.OnProgress != nil {
			req.OnProgress(agent.ProgressEvent{Kind: "tool", Tool: "Bash", Target: "boo ls"})
		}
		return agent.Response{Text: "ok"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex"}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))

	if resp := waitReply(t, replied); resp.Text != "ok" {
		t.Fatalf("answer = %q", resp.Text)
	}
	c := sess.counts()
	if c.done != 1 || c.failed != 0 || c.steps != 1 || c.ack != 0 {
		t.Fatalf("counts = %+v, want {ack:0 steps:1 done:1 failed:0}", c)
	}
}

func TestOrchestrator_PlainBot_WiresFailOnError(t *testing.T) {
	sess := &fakeProgressSession{}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{}, context.DeadlineExceeded
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex"}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))
	_ = waitReply(t, replied)

	c := sess.counts()
	if c.failed != 1 || c.done != 0 {
		t.Fatalf("counts = %+v, want failed:1 done:0", c)
	}
}

func TestOrchestrator_OrchestratorBot_AcksViaSession(t *testing.T) {
	sess := &fakeProgressSession{}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(context.Context, string, agent.Spec, agent.Request) (agent.Response, error) {
		return agent.Response{Text: "answer"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", Orchestrator: true}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))
	if resp := waitReply(t, replied); resp.Text != "answer" {
		t.Fatalf("answer = %q (ack must NOT be a separate text reply)", resp.Text)
	}
	c := sess.counts()
	if c.ack != 1 || c.done != 1 {
		t.Fatalf("counts = %+v, want ack:1 done:1", c)
	}
}

func TestOrchestrator_NilReporter_BehavesAsToday(t *testing.T) {
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.OnProgress != nil {
			t.Error("OnProgress must be nil when no session")
		}
		return agent.Response{Text: "ok"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex"}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{nilSess: true})

	o.HandleMessage(context.Background(), feishuInbound("hello"))
	if resp := waitReply(t, replied); resp.Text != "ok" {
		t.Fatalf("answer = %q", resp.Text)
	}
}

func TestOrchestrator_OrchestratorBot_AckFailureFallsBackToText(t *testing.T) {
	sess := &fakeProgressSession{ackErr: context.DeadlineExceeded}
	replied := make(chan agent.Response, 4)
	gateway := fakeReplyGateway{reply: func(_ context.Context, _ channel.ReplyTarget, resp agent.Response) error {
		replied <- resp
		return nil
	}}
	exec := &fakeExecutor{send: func(_ context.Context, _ string, _ agent.Spec, req agent.Request) (agent.Response, error) {
		if req.OnProgress != nil {
			t.Error("OnProgress must be nil after Ack failure (session disabled)")
		}
		return agent.Response{Text: "answer"}, nil
	}}
	o := NewBotMessageOrchestrator(exec, gateway, fakeResolver{resolve: func(context.Context, string) (agent.Spec, error) {
		return agent.Spec{Type: "codex-exec", Command: "codex", Orchestrator: true}, nil
	}}, nil)
	o.SetProgressReporter(&fakeProgressReporter{sess: sess})

	o.HandleMessage(context.Background(), feishuInbound("hello"))

	// Ack failed → text ack fallback first, then the answer.
	if first := waitReply(t, replied); first.Text != ackReply {
		t.Fatalf("first reply = %q, want text ack %q", first.Text, ackReply)
	}
	if second := waitReply(t, replied); second.Text != "answer" {
		t.Fatalf("second reply = %q, want answer", second.Text)
	}
	c := sess.counts()
	if c.ack != 1 || c.done != 0 || c.steps != 0 {
		t.Fatalf("counts = %+v, want ack:1 done:0 steps:0", c)
	}
}
