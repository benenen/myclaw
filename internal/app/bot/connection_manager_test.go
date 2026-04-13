package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/domain"
)

type runtimeStarterStub struct{}

func (s *runtimeStarterStub) StartRuntime(_ context.Context, _ channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	return &runtimeHandleStub{done: make(chan struct{})}, nil
}

type runtimeHandleStub struct {
	done     chan struct{}
	stopFn   func()
	stopOnce bool
}

func (h *runtimeHandleStub) Stop() {
	if h.stopOnce {
		return
	}
	h.stopOnce = true
	if h.stopFn != nil {
		h.stopFn()
	}
	close(h.done)
}

func (h *runtimeHandleStub) Done() <-chan struct{} {
	return h.done
}

func TestBotConnectionManagerStartMarksBotConnected(t *testing.T) {
	manager := NewBotConnectionManager(nil, nil, &runtimeStarterStub{}, nil)

	if err := manager.Start(context.Background(), "bot_1"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := manager.handles["bot_1"]; !ok {
		t.Fatal("expected bot_1 handle to be tracked after start")
	}
}

func TestBotConnectionManagerRejectsDuplicateStart(t *testing.T) {
	manager := NewBotConnectionManager(nil, nil, &runtimeStarterStub{}, nil)

	if err := manager.Start(context.Background(), "bot_1"); err != nil {
		t.Fatalf("expected first start to succeed, got %v", err)
	}
	if err := manager.Start(context.Background(), "bot_1"); err != ErrRuntimeAlreadyStarted {
		t.Fatalf("expected ErrRuntimeAlreadyStarted, got %v", err)
	}
}

func TestBotConnectionManagerPassesStoredCredentialsToRuntime(t *testing.T) {
	ctx := context.Background()
	starter := &capturingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1"})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte("cipher"), CredentialVersion: 2})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	if starter.req.AccountUID != "wxid_1" {
		t.Fatalf("unexpected account uid: %q", starter.req.AccountUID)
	}
	select {
	case <-starter.ctx.Done():
		t.Fatal("expected runtime to use detached context")
	default:
	}
}

func TestBotConnectionManagerDetachesRuntimeFromRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	starter := &capturingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1"})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte("cipher"), CredentialVersion: 2})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-starter.ctx.Done():
		t.Fatal("expected runtime context to survive request cancellation")
	default:
	}
}

func TestBotConnectionManagerPreservesContextValuesWhenDetachingCancellation(t *testing.T) {
	type ctxKey string
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("request_id"), "req-123"))
	defer cancel()
	starter := &capturingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1"})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte("cipher"), CredentialVersion: 2})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if got := starter.ctx.Value(ctxKey("request_id")); got != "req-123" {
		t.Fatalf("unexpected preserved context value: %#v", got)
	}
	select {
	case <-starter.ctx.Done():
		t.Fatal("expected runtime context to remain detached from cancellation")
	default:
	}
}

func TestBotConnectionManagerUsesBackgroundContextWhenInputIsNil(t *testing.T) {
	starter := &capturingRuntimeStarter{}
	manager := NewBotConnectionManager(nil, nil, starter, nil)

	if err := manager.Start(nil, "bot_1"); err != nil {
		t.Fatal(err)
	}
	if starter.ctx == nil {
		t.Fatal("expected runtime context")
	}
	select {
	case <-starter.ctx.Done():
		t.Fatal("expected background runtime context")
	default:
	}
}

func TestBotConnectionManagerStartDoesNotHoldLockWhileStartingRuntime(t *testing.T) {
	starter := &blockingRuntimeStarterStub{started: make(chan struct{}), release: make(chan struct{})}
	manager := NewBotConnectionManager(nil, nil, starter, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(context.Background(), "bot_1")
	}()

	<-starter.started

	locked := make(chan struct{})
	go func() {
		manager.mu.Lock()
		close(locked)
		manager.mu.Unlock()
	}()

	select {
	case <-locked:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected mutex to be available while runtime start is in progress")
	}

	close(starter.release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected start to succeed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected start to complete after releasing starter")
	}
}

func TestBotConnectionManagerRejectsConcurrentStartWhileRuntimeStarting(t *testing.T) {
	starter := &blockingRuntimeStarterStub{started: make(chan struct{}), release: make(chan struct{})}
	manager := NewBotConnectionManager(nil, nil, starter, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(context.Background(), "bot_1")
	}()

	<-starter.started

	if err := manager.Start(context.Background(), "bot_1"); err != ErrRuntimeAlreadyStarted {
		t.Fatalf("expected ErrRuntimeAlreadyStarted during in-flight start, got %v", err)
	}
	if !manager.Active("bot_1") {
		t.Fatal("expected bot_1 to be treated as active while runtime start is in progress")
	}

	close(starter.release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected first start to succeed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected first start to complete after releasing starter")
	}

	if !manager.Active("bot_1") {
		t.Fatal("expected bot_1 handle to remain tracked after runtime start completes")
	}
}

func TestBotConnectionManagerClearsReservationWhenStartRuntimeFails(t *testing.T) {
	manager := NewBotConnectionManager(nil, nil, &failingRuntimeStarterStub{}, nil)

	if err := manager.Start(context.Background(), "bot_1"); err == nil {
		t.Fatal("expected start to fail")
	}
	if manager.Active("bot_1") {
		t.Fatal("expected failed start reservation to be cleared")
	}
}

func TestBotConnectionManagerLogsMessageAndClearsHandleOnStop(t *testing.T) {
	ctx := context.Background()
	starter := &eventingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1"})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte(`{"openid":"wxid_1"}`), CredentialVersion: 1})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	if !manager.Active("bot_1") {
		t.Fatal("expected active runtime")
	}
	starter.StopLast()
	if manager.Active("bot_1") {
		t.Fatal("expected runtime handle to be cleared")
	}
}

func TestBotConnectionManagerForwardsRuntimeEvent(t *testing.T) {
	ctx := context.Background()
	starter := &capturingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1"})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte(`{"openid":"wxid_1"}`), CredentialVersion: 1})
	var got channel.RuntimeEvent
	manager := NewBotConnectionManagerWithCallbacks(bots, accounts, starter, nil, nil, func(ev channel.RuntimeEvent) {
		got = ev
	})

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	if starter.req.Callbacks.OnEvent == nil {
		t.Fatal("expected runtime event callback")
	}

	expected := channel.RuntimeEvent{BotID: "bot_1", ChannelType: "wechat", MessageID: "msg_1", From: "wxid_sender", Text: "hello", ReplyTarget: channel.ReplyTarget{ChannelType: "wechat", RecipientID: "wxid_sender", Metadata: map[string]string{"token": "token-1"}}}
	starter.req.Callbacks.OnEvent(expected)
	if got.MessageID != expected.MessageID || got.ReplyTarget.RecipientID != expected.ReplyTarget.RecipientID || got.ReplyTarget.MetadataValue("token") != "token-1" {
		t.Fatalf("forwarded event = %#v", got)
	}
}

func TestBotConnectionManagerWithoutEventHandlerStillStarts(t *testing.T) {
	ctx := context.Background()
	starter := &capturingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1"})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte(`{"openid":"wxid_1"}`), CredentialVersion: 1})
	manager := NewBotConnectionManagerWithCallbacks(bots, accounts, starter, nil, nil, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	if starter.req.Callbacks.OnEvent == nil {
		t.Fatal("expected runtime event logger callback")
	}
	starter.req.Callbacks.OnEvent(channel.RuntimeEvent{BotID: "bot_1", MessageID: "msg_1", From: "wxid_sender", Text: "hello"})
}

func TestBotConnectionManagerMarksBotConnectedOnConnectedState(t *testing.T) {
	ctx := context.Background()
	starter := &eventingRuntimeStarter{}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1", ConnectionStatus: domain.BotConnectionStatusConnecting})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte(`{"openid":"wxid_1"}`), CredentialVersion: 1})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	if bots.bot.ConnectionStatus != domain.BotConnectionStatusConnected {
		t.Fatalf("unexpected bot connection status: %s", bots.bot.ConnectionStatus)
	}
}

func TestBotConnectionManagerMarksBotErrorOnErrorState(t *testing.T) {
	ctx := context.Background()
	starter := &eventingRuntimeStarter{emitError: true}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1", ConnectionStatus: domain.BotConnectionStatusConnecting})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte(`{"openid":"wxid_1"}`), CredentialVersion: 1})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return bots.bot.ConnectionStatus == domain.BotConnectionStatusError }, t)
	if bots.bot.ConnectionError != "runtime failed" {
		t.Fatalf("unexpected bot connection error: %s", bots.bot.ConnectionError)
	}
	waitFor(func() bool { return !manager.Active("bot_1") }, t)
}

func TestBotConnectionManagerMarksBotLoginRequiredOnSessionExpired(t *testing.T) {
	ctx := context.Background()
	starter := &eventingRuntimeStarter{emitError: true, err: fmt.Errorf("%w: getupdates failed", wechat.ErrSessionExpired)}
	bots := newBotRepoStub(domain.Bot{ID: "bot_1", ChannelType: "wechat", ChannelAccountID: "acct_1", ConnectionStatus: domain.BotConnectionStatusConnecting})
	accounts := newAccountRepoStub(domain.ChannelAccount{ID: "acct_1", AccountUID: "wxid_1", CredentialCiphertext: []byte(`{"openid":"wxid_1"}`), CredentialVersion: 1})
	manager := NewBotConnectionManager(bots, accounts, starter, nil)

	if err := manager.Start(ctx, "bot_1"); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return bots.bot.ConnectionStatus == domain.BotConnectionStatusLoginRequired }, t)
	if !strings.Contains(bots.bot.ConnectionError, "wechat session expired") {
		t.Fatalf("unexpected session expiry error message: %s", bots.bot.ConnectionError)
	}
	waitFor(func() bool { return !manager.Active("bot_1") }, t)
}

func waitFor(check func() bool, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

type blockingRuntimeStarterStub struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingRuntimeStarterStub) StartRuntime(_ context.Context, _ channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	close(s.started)
	<-s.release
	return &runtimeHandleStub{done: make(chan struct{})}, nil
}

type failingRuntimeStarterStub struct{}

func (s *failingRuntimeStarterStub) StartRuntime(_ context.Context, _ channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	return nil, errors.New("start failed")
}

type capturingRuntimeStarter struct {
	ctx context.Context
	req channel.StartRuntimeRequest
}

func (s *capturingRuntimeStarter) StartRuntime(ctx context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	s.ctx = ctx
	s.req = req
	return &runtimeHandleStub{done: make(chan struct{})}, nil
}

type eventingRuntimeStarter struct {
	lastHandle *runtimeHandleStub
	emitError  bool
	err        error
}

func (s *eventingRuntimeStarter) StartRuntime(_ context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	handle := &runtimeHandleStub{done: make(chan struct{}), stopFn: func() {
		if req.Callbacks.OnState != nil {
			req.Callbacks.OnState(channel.RuntimeStateEvent{BotID: req.BotID, ChannelType: req.ChannelType, State: channel.RuntimeStateStopped})
		}
	}}
	s.lastHandle = handle
	if req.Callbacks.OnEvent != nil {
		req.Callbacks.OnEvent(channel.RuntimeEvent{BotID: req.BotID, ChannelType: req.ChannelType, MessageID: "msg_1", From: req.AccountUID, Text: "hello"})
	}
	if req.Callbacks.OnState != nil {
		req.Callbacks.OnState(channel.RuntimeStateEvent{BotID: req.BotID, ChannelType: req.ChannelType, State: channel.RuntimeStateConnected})
		if s.emitError {
			err := s.err
			if err == nil {
				err = errors.New("runtime failed")
			}
			go req.Callbacks.OnState(channel.RuntimeStateEvent{BotID: req.BotID, ChannelType: req.ChannelType, State: channel.RuntimeStateError, Err: err})
		}
	}
	return handle, nil
}

func (s *eventingRuntimeStarter) StopLast() {
	if s.lastHandle != nil {
		s.lastHandle.Stop()
	}
}

type accountRepoStub struct {
	account domain.ChannelAccount
}

func newAccountRepoStub(account domain.ChannelAccount) *accountRepoStub {
	return &accountRepoStub{account: account}
}

func (r *accountRepoStub) Upsert(context.Context, domain.ChannelAccount) (domain.ChannelAccount, error) {
	panic("unexpected call")
}

func (r *accountRepoStub) GetByID(_ context.Context, id string) (domain.ChannelAccount, error) {
	if r.account.ID != id {
		return domain.ChannelAccount{}, domain.ErrNotFound
	}
	return r.account, nil
}

func (r *accountRepoStub) ListByUserID(context.Context, string, string) ([]domain.ChannelAccount, error) {
	panic("unexpected call")
}

var _ domain.ChannelAccountRepository = (*accountRepoStub)(nil)
