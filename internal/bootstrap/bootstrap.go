package bootstrap

import (
	"context"
	stdhttp "net/http"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	_ "github.com/benenen/myclaw/internal/agent/codex"
	"github.com/benenen/myclaw/internal/api/http/handlers"
	"github.com/benenen/myclaw/internal/api/http/web"
	"github.com/benenen/myclaw/internal/app/bot"
	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/config"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/logging"
	"github.com/benenen/myclaw/internal/security"
	"github.com/benenen/myclaw/internal/store"
	"github.com/benenen/myclaw/internal/store/repositories"
)

const botCLITimeout = 60 * time.Second

type App struct {
	Config  config.Config
	Handler stdhttp.Handler
}

func New(cfg config.Config) (*App, error) {
	logger := logging.New(cfg.LogLevel)

	db, err := store.Open(cfg.SQLitePath)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(db); err != nil {
		return nil, err
	}

	cipher, err := security.NewCipher(cfg.ChannelMasterKey)
	if err != nil {
		return nil, err
	}

	userRepo := repositories.NewUserRepository(db)
	accountRepo := repositories.NewChannelAccountRepository(db)
	bindingRepo := repositories.NewChannelBindingRepository(db)
	botRepo := repositories.NewBotRepository(db)
	capabilityRepo := repositories.NewAgentCapabilityRepository(db)

	wechatCfg := wechat.LoadConfig()
	wechatClient := wechat.NewHTTPClient(wechatCfg, logger)
	provider := wechat.NewProvider(wechatClient, logger)

	executor := agent.NewManager()
	replyGateway := wechat.NewReplyGateway(wechatClient)
	resolver := bot.NewBotCLIResolver(botRepo, capabilityRepo, bot.BotCLIResolverConfig{Timeout: botCLITimeout})
	orchestrator := bot.NewBotMessageOrchestrator(executor, replyGateway, resolver)
	botManager := bot.NewBotConnectionManagerWithCallbacks(botRepo, accountRepo, provider, cipher, logger, func(ev channel.RuntimeEvent) {
		orchestrator.HandleEvent(context.Background(), ev)
	})
	botSvc := bot.NewBotService(userRepo, botRepo, bindingRepo, accountRepo, capabilityRepo, cipher, provider, botManager)

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", web.Handler())

	handlers.RegisterRoutes(mux, handlers.Dependencies{
		BotService: botSvc,
	})

	go func() {
		ctx := context.Background()
		bots, err := botRepo.ListWithAccounts(ctx)
		if err != nil {
			logger.Info("bootstrap runtime restore failed", "error", err)
			return
		}
		for _, item := range bots {
			if item.ChannelAccountID == "" {
				continue
			}
			item.ConnectionStatus = domain.BotConnectionStatusConnecting
			if _, err := botRepo.Update(ctx, item); err != nil {
				logger.Info("bootstrap bot restore status update failed", "bot_id", item.ID, "error", err)
				continue
			}
			if err := botManager.Start(ctx, item.ID); err != nil {
				logger.Info("bootstrap bot runtime start failed", "bot_id", item.ID, "error", err)
			}
		}
	}()

	return &App{
		Config:  cfg,
		Handler: mux,
	}, nil
}
