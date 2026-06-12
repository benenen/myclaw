package bootstrap

import (
	"context"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/benenen/myclaw/internal/agent"
	_ "github.com/benenen/myclaw/internal/agent/claude"
	_ "github.com/benenen/myclaw/internal/agent/codex"
	_ "github.com/benenen/myclaw/internal/agent/opencode"
	"github.com/benenen/myclaw/internal/api/http/handlers"
	"github.com/benenen/myclaw/internal/api/http/web"
	"github.com/benenen/myclaw/internal/app/bot"
	"github.com/benenen/myclaw/internal/app/capability"
	"github.com/benenen/myclaw/internal/app/orchestration"
	"github.com/benenen/myclaw/internal/channel"
	"github.com/benenen/myclaw/internal/channel/httpchan"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/config"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/hook"
	"github.com/benenen/myclaw/internal/logging"
	"github.com/benenen/myclaw/internal/security"
	"github.com/benenen/myclaw/internal/store"
	"github.com/benenen/myclaw/internal/store/repositories"
)

const (
	botCLITimeout = 60 * time.Minute
)

type App struct {
	Config  config.Config
	Handler stdhttp.Handler
}

func New(cfg config.Config) (*App, error) {
	logger := logging.New(cfg.LogLevel)

	if cfg.DataDir != "" {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return nil, err
		}
	}
	if cfg.SQLitePath != "" && cfg.SQLitePath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755); err != nil {
			return nil, err
		}
	}

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
	registeredAgentRepo := repositories.NewRegisteredAgentRepository(db)

	wechatCfg := wechat.LoadConfig()
	wechatClient := wechat.NewHTTPClient(wechatCfg, logger)
	wechatProvider := wechat.NewProvider(wechatClient, logger)
	wechatReplyGateway := wechat.NewReplyGateway(wechatClient)

	httpReceiver := httpchan.NewReceiver()
	httpProvider := httpchan.NewProvider(httpReceiver)
	httpReplyGateway := httpchan.NewReplyGateway()

	multiProvider := channel.NewMultiProvider()
	multiProvider.Register("wechat", wechatProvider, wechatProvider)
	multiProvider.Register(httpchan.ChannelType, httpProvider, httpProvider)

	multiReplyGateway := channel.NewMultiReplyGateway()
	multiReplyGateway.Register("wechat", wechatReplyGateway)
	multiReplyGateway.Register(httpchan.ChannelType, httpReplyGateway)

	executor := agent.NewManager()
	resolver := bot.NewBotCLIResolver(botRepo, capabilityRepo, bot.BotCLIResolverConfig{
		Timeout:             botCLITimeout,
		WorkspaceRoot:       cfg.BotWorkspaceRoot(),
		SQLitePath:          cfg.SQLitePath,
		OrchestratorTimeout: cfg.OrchestratorTimeout,
		MCPURL:              cfg.MCPURL,
		OrchestratorPrompt:  orchestration.OrchestratorPrompt(),
	})
	orchestrator := bot.NewBotMessageOrchestrator(executor, multiReplyGateway, resolver)

	taskStore := orchestration.NewTaskStore()
	localRunner := orchestration.NewLocalRunner(resolver, executor)
	runner := orchestration.NewRunner(localRunner, orchestration.NewA2AClient(nil))
	mcpService := orchestration.NewMCPService(registeredAgentRepo, taskStore, runner)
	messageSimulator := bot.NewMessageSimulator(botRepo, accountRepo, cipher, orchestrator)
	hookManager := hook.NewManager(botRepo, resolver, executor)
	botManager := bot.NewBotConnectionManagerWithCallbacks(botRepo, accountRepo, multiProvider, cipher, logger, func(ev channel.RuntimeEvent) {
		orchestrator.HandleEvent(context.Background(), ev)
	})
	botSvc := bot.NewBotService(userRepo, botRepo, bindingRepo, accountRepo, capabilityRepo, cipher, multiProvider, botManager)
	discoverer := capability.NewAgentCapabilityDiscoverer(capabilityRepo, nil)
	botSvc.SetCapabilityDiscoverer(discoverer)

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", web.Handler())

	mcpHandler := orchestration.NewMCPHandler(mcpService)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	mux.Handle("POST /a2a/register", orchestration.RegisterHandler(registeredAgentRepo))
	mux.Handle("POST /a2a/heartbeat", orchestration.HeartbeatHandler(registeredAgentRepo))

	handlers.RegisterRoutes(mux, handlers.Dependencies{
		BotService:       botSvc,
		MessageSimulator: messageSimulator,
		HookManager:      hookManager,
		HttpReceiver:     httpReceiver,
	})

	if _, err := discoverer.Refresh(context.Background()); err != nil {
		logger.Info("agent capability refresh failed", "error", err)
	}

	if err := orchestration.SyncLocalAgents(context.Background(), botRepo, registeredAgentRepo); err != nil {
		logger.Info("local sub-agent registration failed", "error", err)
	}

	orchestration.StartHealthSweeper(context.Background(), registeredAgentRepo, 90*time.Second, 30*time.Second)

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
