package bootstrap

import (
	stdhttp "net/http"

	"github.com/benenen/channel-plugin/internal/api/http/handlers"
	"github.com/benenen/channel-plugin/internal/app"
	"github.com/benenen/channel-plugin/internal/channel/wechat"
	"github.com/benenen/channel-plugin/internal/config"
	"github.com/benenen/channel-plugin/internal/security"
	"github.com/benenen/channel-plugin/internal/store"
	"github.com/benenen/channel-plugin/internal/store/repositories"
)

type App struct {
	Config  config.Config
	Handler stdhttp.Handler
}

func New(cfg config.Config) (*App, error) {
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
	appKeyRepo := repositories.NewAppKeyRepository(db)

	// Provider
	wechatCfg := wechat.LoadConfig()
	wechatClient := wechat.NewHTTPClient(wechatCfg)
	provider := wechat.NewProvider(wechatClient)

	// Application services
	bindingSvc := app.NewBindingService(userRepo, bindingRepo, accountRepo, cipher, provider)
	appKeySvc := app.NewAppKeyService(appKeyRepo, accountRepo)
	runtimeSvc := app.NewRuntimeService(appKeyRepo, accountRepo, cipher, provider)
	accountQuerySvc := app.NewChannelAccountQueryService(userRepo, accountRepo, appKeyRepo)

	// HTTP
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	handlers.RegisterRoutes(mux, handlers.Dependencies{
		BindingService:      bindingSvc,
		AppKeyService:       appKeySvc,
		RuntimeService:      runtimeSvc,
		AccountQueryService: accountQuerySvc,
	})

	return &App{
		Config:  cfg,
		Handler: mux,
	}, nil
}
