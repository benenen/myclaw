package bootstrap

import (
	"context"
	stdhttp "net/http"

	"github.com/benenen/myclaw/internal/api/http/handlers"
	"github.com/benenen/myclaw/internal/api/http/web"
	"github.com/benenen/myclaw/internal/app"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/config"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/logging"
	"github.com/benenen/myclaw/internal/security"
	"github.com/benenen/myclaw/internal/store"
	"github.com/benenen/myclaw/internal/store/repositories"
)

type App struct {
	Config  config.Config
	Handler stdhttp.Handler
}

func New(cfg config.Config) (*App, error) {
	logger := logging.New(cfg.LogLevel)

	// Database
	db, err := store.Open(cfg.SQLitePath)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(db); err != nil {
		return nil, err
	}

	// Security
	cipher, err := security.NewCipher(cfg.ChannelMasterKey)
	if err != nil {
		return nil, err
	}

	// Repositories
	userRepo := repositories.NewUserRepository(db)
	accountRepo := repositories.NewChannelAccountRepository(db)
	bindingRepo := repositories.NewChannelBindingRepository(db)
	botRepo := repositories.NewBotRepository(db)

	// Provider
	wechatCfg := wechat.LoadConfig()
	wechatClient := wechat.NewHTTPClient(wechatCfg, logger)
	provider := wechat.NewProvider(wechatClient, logger)

	// Application services
	botManager := app.NewBotConnectionManagerWithCipher(botRepo, accountRepo, provider, cipher, logger)
	botSvc := app.NewBotService(userRepo, botRepo, bindingRepo, accountRepo, cipher, provider, botManager)

	// HTTP
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Web admin UI
	mux.Handle("/", web.Handler())

	handlers.RegisterRoutes(mux, handlers.Dependencies{
		BotService: botSvc,
	})

	go func() {
		ctx := context.Background()
		bots, err := botRepo.ListWithAccounts(ctx)
		if err != nil {
			return
		}
		for _, bot := range bots {
			if bot.ChannelAccountID == "" {
				continue
			}
			bot.ConnectionStatus = domain.BotConnectionStatusConnecting
			if _, err := botRepo.Update(ctx, bot); err != nil {
				continue
			}
			_ = botManager.Start(ctx, bot.ID)
		}
	}()

	return &App{
		Config:  cfg,
		Handler: mux,
	}, nil
}
