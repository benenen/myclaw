package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type stubDriver struct {
	run func(context.Context, Spec, Request) (Response, error)
}

func (d stubDriver) Run(ctx context.Context, spec Spec, req Request) (Response, error) {
	return d.run(ctx, spec, req)
}

func TestManagerSendDelegatesToBotSession(t *testing.T) {
	mgr := NewManager(stubDriver{run: func(_ context.Context, _ Spec, req Request) (Response, error) {
		return Response{Text: "reply:" + req.Prompt}, nil
	}})

	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "reply:hello" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if mgr.State("bot-1") != SessionStateReady {
		t.Fatalf("State() = %s", mgr.State("bot-1"))
	}
}

func TestManagerMarksBrokenAfterDriverError(t *testing.T) {
	mgr := NewManager(stubDriver{run: func(context.Context, Spec, Request) (Response, error) {
		return Response{}, errors.New("boom")
	}})

	_, _ = mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "hello"})
	if mgr.State("bot-1") != SessionStateBroken {
		t.Fatalf("State() = %s", mgr.State("bot-1"))
	}
}

func TestManagerRecreatesBrokenSession(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	mgr := NewManager(stubDriver{run: func(_ context.Context, _ Spec, req Request) (Response, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return Response{}, errors.New("boom")
		}
		return Response{Text: "reply:" + req.Prompt}, nil
	}})

	_, _ = mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "first"})
	resp, err := mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "reply:second" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}
	if mgr.State("bot-1") != SessionStateReady {
		t.Fatalf("State() = %s", mgr.State("bot-1"))
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestManagerSessionForDoesNotBlockManagerOnBusySession(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	mgr := NewManager(stubDriver{run: func(_ context.Context, _ Spec, req Request) (Response, error) {
		if req.Prompt == "first" {
			close(started)
			<-release
		}
		return Response{Text: req.Prompt}, nil
	}})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = mgr.Send(context.Background(), "bot-1", Spec{Command: "codex"}, Request{Prompt: "first"})
	}()

	<-started

	tookManager := make(chan struct{})
	go func() {
		mgr.mu.Lock()
		mgr.mu.Unlock()
		close(tookManager)
	}()

	select {
	case <-tookManager:
	case <-firstDone:
		t.Fatal("busy send finished before manager lock check")
	}

	close(release)
	<-firstDone
}

func TestSessionSendSerializesConcurrentCalls(t *testing.T) {
	firstStarted := make(chan struct{})
	allowFirstToFinish := make(chan struct{})
	secondStarted := make(chan struct{})
	allowSecondToFinish := make(chan struct{})
	finished := make(chan string, 2)

	session := NewSession(stubDriver{run: func(_ context.Context, _ Spec, req Request) (Response, error) {
		switch req.Prompt {
		case "first":
			close(firstStarted)
			<-allowFirstToFinish
		case "second":
			close(secondStarted)
			<-allowSecondToFinish
		default:
			t.Fatalf("unexpected prompt %q", req.Prompt)
		}
		return Response{Text: req.Prompt}, nil
	}}, Spec{Command: "codex"})

	go func() {
		resp, err := session.Send(context.Background(), Request{Prompt: "first"})
		if err != nil {
			finished <- "first-error"
			return
		}
		finished <- resp.Text
	}()

	<-firstStarted

	secondReturned := make(chan struct{})
	go func() {
		defer close(secondReturned)
		resp, err := session.Send(context.Background(), Request{Prompt: "second"})
		if err != nil {
			finished <- "second-error"
			return
		}
		finished <- resp.Text
	}()

	select {
	case <-secondStarted:
		t.Fatal("second driver run started before first completed")
	case <-secondReturned:
		t.Fatal("second send returned before first completed")
	default:
	}

	close(allowFirstToFinish)
	if got := <-finished; got != "first" {
		t.Fatalf("first result = %q", got)
	}
	<-secondStarted

	close(allowSecondToFinish)
	if got := <-finished; got != "second" {
		t.Fatalf("second result = %q", got)
	}
	<-secondReturned

	if session.State() != SessionStateReady {
		t.Fatalf("State() = %s", session.State())
	}
}

func TestSessionSendUsesClonedSpecSnapshot(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	usedSpec := make(chan Spec, 1)

	spec := Spec{
		Command: "codex",
		Args:    []string{"--fast"},
		Env:     map[string]string{"MODE": "fast"},
	}
	session := NewSession(stubDriver{run: func(_ context.Context, spec Spec, req Request) (Response, error) {
		close(started)
		<-release
		usedSpec <- spec
		return Response{Text: spec.Command + ":" + req.Prompt}, nil
	}}, spec)

	spec.Command = "claude"
	spec.Args[0] = "--slow"
	spec.Env["MODE"] = "slow"

	done := make(chan Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := session.Send(context.Background(), Request{Prompt: "first"})
		done <- resp
		errCh <- err
	}()

	<-started
	close(release)

	resp := <-done
	if err := <-errCh; err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if resp.Text != "codex:first" {
		t.Fatalf("resp.Text = %q", resp.Text)
	}

	gotSpec := <-usedSpec
	if gotSpec.Command != "codex" {
		t.Fatalf("driver spec command = %q", gotSpec.Command)
	}
	if len(gotSpec.Args) != 1 || gotSpec.Args[0] != "--fast" {
		t.Fatalf("driver spec args = %#v", gotSpec.Args)
	}
	if gotSpec.Env["MODE"] != "fast" {
		t.Fatalf("driver spec env = %#v", gotSpec.Env)
	}
}

func TestManagerRecreatesSessionWhenSpecChanges(t *testing.T) {
	var (
		mu    sync.Mutex
		specs []Spec
	)
	mgr := NewManager(stubDriver{run: func(_ context.Context, spec Spec, req Request) (Response, error) {
		mu.Lock()
		specs = append(specs, spec)
		mu.Unlock()
		return Response{Text: spec.Command + ":" + req.Prompt}, nil
	}})

	firstSpec := Spec{Command: "codex", Args: []string{"--fast"}}
	secondSpec := Spec{Command: "claude", Args: []string{"--slow"}}

	firstResp, err := mgr.Send(context.Background(), "bot-1", firstSpec, Request{Prompt: "one"})
	if err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	secondResp, err := mgr.Send(context.Background(), "bot-1", secondSpec, Request{Prompt: "two"})
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}

	if firstResp.Text != "codex:one" {
		t.Fatalf("firstResp.Text = %q", firstResp.Text)
	}
	if secondResp.Text != "claude:two" {
		t.Fatalf("secondResp.Text = %q", secondResp.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d", len(specs))
	}
	if specs[0].Command != "codex" || specs[1].Command != "claude" {
		t.Fatalf("specs = %#v", specs)
	}
}

func TestManagerSpecReplacementCreatesNewSessionForInFlightSend(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	mgr := NewManager(stubDriver{run: func(_ context.Context, spec Spec, req Request) (Response, error) {
		if req.Prompt == "first" {
			close(started)
			<-release
		}
		return Response{Text: spec.Command + ":" + req.Prompt}, nil
	}})

	firstSpec := Spec{Command: "codex"}
	secondSpec := Spec{Command: "claude"}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, err := mgr.Send(context.Background(), "bot-1", firstSpec, Request{Prompt: "first"})
		if err != nil {
			t.Errorf("first Send() error = %v", err)
			return
		}
		if resp.Text != "codex:first" {
			t.Errorf("first resp.Text = %q", resp.Text)
		}
	}()

	<-started

	resp, err := mgr.Send(context.Background(), "bot-1", secondSpec, Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("replacement Send() error = %v", err)
	}
	if resp.Text != "claude:second" {
		t.Fatalf("replacement resp.Text = %q", resp.Text)
	}
	if mgr.State("bot-1") != SessionStateReady {
		t.Fatalf("State() = %s", mgr.State("bot-1"))
	}

	close(release)
	<-firstDone
}

func TestManagerRetriesAfterBrokenSessionWithoutTimingFlakes(t *testing.T) {
	allowFailureReturn := make(chan struct{})
	startedFailure := make(chan struct{})
	failed := make(chan struct{})
	var mu sync.Mutex
	calls := 0

	mgr := NewManager(stubDriver{run: func(_ context.Context, spec Spec, req Request) (Response, error) {
		mu.Lock()
		calls++
		callNumber := calls
		mu.Unlock()

		if callNumber == 1 {
			close(startedFailure)
			<-allowFailureReturn
			return Response{}, errors.New("boom")
		}
		return Response{Text: spec.Command + ":" + req.Prompt}, nil
	}})

	spec := Spec{Command: "codex"}
	go func() {
		defer close(failed)
		_, _ = mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "first"})
	}()

	<-startedFailure
	if mgr.State("bot-1") != SessionStateBusy {
		t.Fatalf("State() before failure = %s", mgr.State("bot-1"))
	}

	close(allowFailureReturn)
	<-failed
	if mgr.State("bot-1") != SessionStateBroken {
		t.Fatalf("State() after failure = %s", mgr.State("bot-1"))
	}

	resp, err := mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("retry Send() error = %v", err)
	}
	if resp.Text != "codex:second" {
		t.Fatalf("retry resp.Text = %q", resp.Text)
	}
	if mgr.State("bot-1") != SessionStateReady {
		t.Fatalf("State() after retry = %s", mgr.State("bot-1"))
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestManagerConcurrentRetriesReuseSingleReplacementAfterBrokenSession(t *testing.T) {
	var (
		mu             sync.Mutex
		calls          int
		successStarted = make(chan struct{})
		releaseSuccess = make(chan struct{})
	)

	mgr := NewManager(stubDriver{run: func(_ context.Context, spec Spec, req Request) (Response, error) {
		mu.Lock()
		calls++
		callNumber := calls
		mu.Unlock()

		switch callNumber {
		case 1:
			return Response{}, errors.New("boom")
		case 2:
			close(successStarted)
			<-releaseSuccess
			return Response{Text: spec.Command + ":" + req.Prompt}, nil
		default:
			return Response{Text: spec.Command + ":" + req.Prompt}, nil
		}
	}})

	spec := Spec{Command: "codex"}
	_, _ = mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: "first"})
	if mgr.State("bot-1") != SessionStateBroken {
		t.Fatalf("State() after failure = %s", mgr.State("bot-1"))
	}

	results := make(chan result, 2)

	for _, prompt := range []string{"second", "third"} {
		go func(prompt string) {
			resp, err := mgr.Send(context.Background(), "bot-1", spec, Request{Prompt: prompt})
			results <- result{resp: resp, err: err}
		}(prompt)
	}

	<-successStarted

	mu.Lock()
	if calls != 2 {
		mu.Unlock()
		t.Fatalf("calls while replacement busy = %d", calls)
	}
	mu.Unlock()

	close(releaseSuccess)

	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent retry error = %v", got.err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatalf("calls after concurrent retries = %d", calls)
	}
}

func TestManagerMixedSpecOverlapReusesLatestSession(t *testing.T) {
	var (
		mu         sync.Mutex
		calls      int
		oldStarted = make(chan struct{})
		releaseOld = make(chan struct{})
		newStarted = make(chan struct{})
		releaseNew = make(chan struct{})
	)

	mgr := NewManager(stubDriver{run: func(_ context.Context, spec Spec, req Request) (Response, error) {
		mu.Lock()
		calls++
		callNumber := calls
		mu.Unlock()

		switch {
		case spec.Command == "codex":
			close(oldStarted)
			<-releaseOld
		case spec.Command == "claude" && callNumber == 2:
			close(newStarted)
			<-releaseNew
		}
		return Response{Text: spec.Command + ":" + req.Prompt}, nil
	}})

	oldSpec := Spec{Command: "codex"}
	newSpec := Spec{Command: "claude"}

	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		_, _ = mgr.Send(context.Background(), "bot-1", oldSpec, Request{Prompt: "old"})
	}()

	<-oldStarted

	newResults := make(chan result, 2)
	for _, prompt := range []string{"new-1", "new-2"} {
		go func(prompt string) {
			resp, err := mgr.Send(context.Background(), "bot-1", newSpec, Request{Prompt: prompt})
			newResults <- result{resp: resp, err: err}
		}(prompt)
	}

	<-newStarted

	mu.Lock()
	if calls != 2 {
		mu.Unlock()
		t.Fatalf("calls while latest session busy = %d", calls)
	}
	mu.Unlock()

	close(releaseNew)
	close(releaseOld)
	<-oldDone

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		got := <-newResults
		if got.err != nil {
			t.Fatalf("new spec send error = %v", got.err)
		}
		seen[got.resp.Text] = true
	}
	if !seen["claude:new-1"] || !seen["claude:new-2"] {
		t.Fatalf("new spec responses = %#v", seen)
	}
	if mgr.State("bot-1") != SessionStateReady {
		t.Fatalf("State() after mixed-spec overlap = %s", mgr.State("bot-1"))
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatalf("calls after mixed-spec overlap = %d", calls)
	}
}

type result struct {
	resp Response
	err  error
}
