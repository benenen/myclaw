package app

import (
	"context"
	"errors"
	"testing"

	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/security"
	"github.com/benenen/myclaw/internal/store/repositories"
	"github.com/benenen/myclaw/internal/testutil"
)

type failingRuntimeStarter struct{}

func (failingRuntimeStarter) StartRuntime(context.Context, channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	return nil, errors.New("start runtime failed")
}

type runtimeStarterOverrideProvider struct {
	*wechat.FakeProvider
	starter channel.RuntimeStarter
}

func (p runtimeStarterOverrideProvider) StartRuntime(ctx context.Context, req channel.StartRuntimeRequest) (channel.RuntimeHandle, error) {
	return p.starter.StartRuntime(ctx, req)
}

var _ channel.RuntimeStarter = runtimeStarterOverrideProvider{}

func newTestBotServiceWithRuntimeStarter(t *testing.T, starter channel.RuntimeStarter) (*BotService, *wechat.FakeProvider) {
	t.Helper()
	db := testutil.OpenTestDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, _ := security.NewCipher(key)
	baseProvider := wechat.NewFakeProvider()
	provider := runtimeStarterOverrideProvider{FakeProvider: baseProvider, starter: starter}
	bots := repositories.NewBotRepository(db)
	accounts := repositories.NewChannelAccountRepository(db)
	runtimes := NewBotConnectionManagerWithCipher(bots, accounts, provider, cipher)

	return NewBotService(
		repositories.NewUserRepository(db),
		bots,
		repositories.NewChannelBindingRepository(db),
		accounts,
		cipher,
		provider,
		runtimes,
	), baseProvider
}

func newTestBotServiceWithProvider(t *testing.T, provider channel.Provider) *BotService {
	t.Helper()
	db := testutil.OpenTestDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, _ := security.NewCipher(key)
	bots := repositories.NewBotRepository(db)
	accounts := repositories.NewChannelAccountRepository(db)
	starter, _ := provider.(channel.RuntimeStarter)
	runtimes := NewBotConnectionManagerWithCipher(bots, accounts, starter, cipher)

	return NewBotService(
		repositories.NewUserRepository(db),
		bots,
		repositories.NewChannelBindingRepository(db),
		accounts,
		cipher,
		provider,
		runtimes,
	)
}

func newTestBotService(t *testing.T) *BotService {
	provider := wechat.NewFakeProvider()
	return newTestBotServiceWithProvider(t, provider)
}

func newTestBotServiceAndProvider(t *testing.T) (*BotService, *wechat.FakeProvider) {
	provider := wechat.NewFakeProvider()
	return newTestBotServiceWithProvider(t, provider), provider
}



func TestBotServiceCreateBot(t *testing.T) {
	svc := newTestBotService(t)

	got, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.BotID == "" {
		t.Fatal("expected bot id")
	}
	if got.Name != "sales-bot" {
		t.Fatalf("unexpected bot name: %s", got.Name)
	}
	if got.ChannelType != "wechat" {
		t.Fatalf("unexpected channel type: %s", got.ChannelType)
	}
	if got.ChannelAccountID != "" {
		t.Fatalf("unexpected channel account id: %s", got.ChannelAccountID)
	}
	if got.ConnectionStatus != domain.BotConnectionStatusLoginRequired {
		t.Fatalf("unexpected connection status: %s", got.ConnectionStatus)
	}

	stored, err := svc.bots.GetByID(context.Background(), got.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "sales-bot" {
		t.Fatalf("unexpected stored bot name: %s", stored.Name)
	}
	if stored.ChannelType != "wechat" {
		t.Fatalf("unexpected stored channel type: %s", stored.ChannelType)
	}
	if stored.ConnectionStatus != domain.BotConnectionStatusLoginRequired {
		t.Fatalf("unexpected stored connection status: %s", stored.ConnectionStatus)
	}
}

func TestBotServiceCreateBotRejectsEmptyInput(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.CreateBot(context.Background(), CreateBotInput{})
	if err != domain.ErrInvalidArg {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}

func TestBotServiceListBots(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListBots(context.Background(), "u_123")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(items))
	}
	if items[0].Name != "sales-bot" {
		t.Fatalf("unexpected bot name: %s", items[0].Name)
	}
	if items[0].ChannelType != "wechat" {
		t.Fatalf("unexpected channel type: %s", items[0].ChannelType)
	}
	if items[0].ConnectionStatus != domain.BotConnectionStatusLoginRequired {
		t.Fatalf("unexpected connection status: %s", items[0].ConnectionStatus)
	}
	if items[0].ChannelAccountID != "" {
		t.Fatalf("unexpected channel account id: %s", items[0].ChannelAccountID)
	}
}

func TestBotServiceListBotsReturnsLatestState(t *testing.T) {
	svc := newTestBotService(t)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.bots.GetByID(context.Background(), bot.BotID)
	if err != nil {
		t.Fatal(err)
	}
	stored.ConnectionStatus = domain.BotConnectionStatusConnected
	stored.ChannelAccountID = "acct_1"
	if _, err := svc.bots.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListBots(context.Background(), "u_123")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ConnectionStatus != domain.BotConnectionStatusConnected {
		t.Fatalf("unexpected connection status: %s", items[0].ConnectionStatus)
	}
	if items[0].ChannelAccountID != "acct_1" {
		t.Fatalf("unexpected channel account id: %s", items[0].ChannelAccountID)
	}
}

func TestBotServiceListBotsIsEmptyForNewUser(t *testing.T) {
	svc := newTestBotService(t)
	items, err := svc.ListBots(context.Background(), "u_new")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 bots, got %d", len(items))
	}
}

func TestBotServiceListBotsSeparatesUsers(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_456",
		Name:           "ops-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListBots(context.Background(), "u_123")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(items))
	}
	if items[0].Name != "sales-bot" {
		t.Fatalf("unexpected bot name: %s", items[0].Name)
	}
}

func TestBotServiceListBotsRejectsEmptyUser(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.ListBots(context.Background(), "")
	if err != domain.ErrInvalidArg {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}

func TestBotServiceDeleteBotRemovesBotAndBindings(t *testing.T) {
	svc := newTestBotService(t)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteBot(context.Background(), bot.BotID); err != nil {
		t.Fatal(err)
	}

	_, err = svc.bots.GetByID(context.Background(), bot.BotID)
	if err != domain.ErrNotFound {
		t.Fatalf("expected bot to be deleted, got %v", err)
	}
	_, err = svc.bindings.GetByID(context.Background(), started.BindingID)
	if err != domain.ErrNotFound {
		t.Fatalf("expected bindings to be deleted, got %v", err)
	}
}

func TestBotServiceDeleteBotRejectsEmptyID(t *testing.T) {
	svc := newTestBotService(t)
	err := svc.DeleteBot(context.Background(), "")
	if err != domain.ErrInvalidArg {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}

func TestBotServiceStartLogin(t *testing.T) {
	svc := newTestBotService(t)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}
	if got.BotID != bot.BotID {
		t.Fatalf("unexpected bot id: %s", got.BotID)
	}
	if got.BindingID == "" {
		t.Fatal("expected binding id")
	}
	if got.Status != domain.BindingStatusQRReady {
		t.Fatalf("unexpected binding status: %s", got.Status)
	}
	if got.QRCodePayload == "" {
		t.Fatal("expected qr code payload")
	}
	if got.ExpiresAt == nil {
		t.Fatal("expected expires at")
	}

	binding, err := svc.bindings.GetByID(context.Background(), got.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.BotID != bot.BotID {
		t.Fatalf("unexpected binding bot id: %s", binding.BotID)
	}
	if binding.ChannelType != "wechat" {
		t.Fatalf("unexpected binding channel type: %s", binding.ChannelType)
	}
	if binding.Status != domain.BindingStatusQRReady {
		t.Fatalf("unexpected binding status: %s", binding.Status)
	}
	if binding.ProviderBindingRef == "" {
		t.Fatal("expected provider binding ref")
	}

	stored, err := svc.bots.GetByID(context.Background(), bot.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "sales-bot" {
		t.Fatalf("unexpected bot name: %s", stored.Name)
	}
	if stored.ChannelAccountID != "" {
		t.Fatalf("unexpected channel account id: %s", stored.ChannelAccountID)
	}
	if stored.ConnectionStatus != domain.BotConnectionStatusLoginRequired {
		t.Fatalf("unexpected connection status: %s", stored.ConnectionStatus)
	}
}

func TestBotServiceStartLoginRejectsEmptyBotID(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.StartLogin(context.Background(), StartBotLoginInput{})
	if err != domain.ErrInvalidArg {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}

func TestBotServiceStartLoginFailsForMissingBot(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: "bot_missing"})
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBotServiceRefreshLoginConfirmsBotAndLinksAccount(t *testing.T) {
	svc := newTestBotService(t)
	provider := svc.provider.(*wechat.FakeProvider)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.bindings.GetByID(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	provider.SimulateConfirm(binding.ProviderBindingRef)

	got, err := svc.RefreshLogin(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BindingStatusConfirmed {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	if got.ChannelAccountID == "" {
		t.Fatal("expected channel account id")
	}
	if got.ConnectionStatus != domain.BotConnectionStatusConnected {
		t.Fatalf("unexpected connection status: %s", got.ConnectionStatus)
	}

	storedBot, err := svc.bots.GetByID(context.Background(), bot.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBot.ChannelAccountID == "" {
		t.Fatal("expected bot channel account id")
	}
	if storedBot.ConnectionStatus != domain.BotConnectionStatusConnected {
		t.Fatalf("unexpected bot connection status: %s", storedBot.ConnectionStatus)
	}
}

func TestBotServiceRefreshLoginStartsRuntimeAfterConfirm(t *testing.T) {
	svc, provider := newTestBotServiceAndProvider(t)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "bot one",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}

	started, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.bindings.GetByID(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	provider.SimulateConfirm(binding.ProviderBindingRef)

	if _, err := svc.RefreshLogin(context.Background(), started.BindingID); err != nil {
		t.Fatal(err)
	}

	if !provider.RuntimeStarted(bot.BotID) {
		t.Fatal("expected runtime to start after confirmed login")
	}
}

func TestBotServiceRefreshLoginMarksBotErrorWhenRuntimeStartFails(t *testing.T) {
	svc, provider := newTestBotServiceWithRuntimeStarter(t, failingRuntimeStarter{})
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "bot one",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}

	started, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.bindings.GetByID(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	provider.SimulateConfirm(binding.ProviderBindingRef)

	_, err = svc.RefreshLogin(context.Background(), started.BindingID)
	if err == nil || err.Error() != "start runtime failed" {
		t.Fatalf("expected runtime start failure, got %v", err)
	}

	storedBot, err := svc.bots.GetByID(context.Background(), bot.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBot.ConnectionStatus != domain.BotConnectionStatusError {
		t.Fatalf("unexpected bot connection status: %s", storedBot.ConnectionStatus)
	}
	if storedBot.ConnectionError != "start runtime failed" {
		t.Fatalf("unexpected bot connection error: %s", storedBot.ConnectionError)
	}

	storedBinding, err := svc.bindings.GetByID(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBinding.Status != domain.BindingStatusConfirmed {
		t.Fatalf("unexpected binding status: %s", storedBinding.Status)
	}
	if storedBinding.ChannelAccountID == "" {
		t.Fatal("expected binding channel account id")
	}
}

func TestBotServiceRefreshLoginReturnsQRReadyBeforeConfirm(t *testing.T) {
	svc := newTestBotService(t)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.RefreshLogin(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BindingStatusQRReady {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}

func TestBotServiceRefreshLoginRejectsEmptyBindingID(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.RefreshLogin(context.Background(), "")
	if err != domain.ErrInvalidArg {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}

func TestBotServiceRefreshLoginFailsForMissingBinding(t *testing.T) {
	svc := newTestBotService(t)
	_, err := svc.RefreshLogin(context.Background(), "bind_missing")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBotServiceRefreshLoginMarksBotErrorOnProviderFailure(t *testing.T) {
	svc := newTestBotService(t)
	bot, err := svc.CreateBot(context.Background(), CreateBotInput{
		ExternalUserID: "u_123",
		Name:           "sales-bot",
		ChannelType:    "wechat",
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartLogin(context.Background(), StartBotLoginInput{BotID: bot.BotID})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.bindings.GetByID(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	binding.ProviderBindingRef = "wxbind_missing"
	if _, err := svc.bindings.Update(context.Background(), binding); err != nil {
		t.Fatal(err)
	}

	got, err := svc.RefreshLogin(context.Background(), started.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BindingStatusExpired {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	storedBot, err := svc.bots.GetByID(context.Background(), bot.BotID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBot.ConnectionStatus != domain.BotConnectionStatusError {
		t.Fatalf("unexpected bot connection status: %s", storedBot.ConnectionStatus)
	}
}
