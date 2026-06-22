package handlers

import (
	"context"
	stdhttp "net/http"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	botapp "github.com/benenen/myclaw/internal/app/bot"
	"github.com/benenen/myclaw/internal/channel/httpchan"
	"github.com/benenen/myclaw/internal/hook"
)

type MessageSimulator interface {
	Simulate(ctx context.Context, input botapp.SimulateMessageInput) (botapp.SimulateMessageOutput, error)
}

type Dependencies struct {
	BotService       *botapp.BotService
	MessageSimulator MessageSimulator
	HookManager      *hook.Manager
	HttpReceiver     *httpchan.Receiver
}

func RegisterRoutes(mux *stdhttp.ServeMux, deps Dependencies) {
	wrap := func(h stdhttp.HandlerFunc) stdhttp.Handler {
		return httpapi.RequestIDMiddleware()(h)
	}

	mux.Handle("POST /api/v1/bots/create", wrap(CreateBot(deps.BotService)))
	mux.Handle("GET /api/v1/bots/list", wrap(ListBots(deps.BotService)))
	mux.Handle("POST /api/v1/bots/agent", wrap(ConfigureBotAgent(deps.BotService)))
	mux.Handle("POST /api/v1/bots/connect", wrap(ConnectBot(deps.BotService)))
	mux.Handle("POST /api/v1/bots/delete", wrap(DeleteBot(deps.BotService)))
	mux.Handle("POST /api/v1/bots/simulate-message", wrap(SimulateBotMessage(deps.MessageSimulator)))
	mux.Handle("GET /api/v1/bots/connect", wrap(RefreshBotLogin(deps.BotService)))
	mux.Handle("GET /api/v1/agent-capabilities", wrap(ListAgentCapabilities(deps.BotService)))
	mux.Handle("GET /api/v1/mcp-servers", wrap(ListMCPServers(deps.BotService)))

	if deps.HookManager != nil {
		mux.Handle("POST /hooks/{platform}/{botname}", wrap(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			deps.HookManager.HandleHook(w, r, r.PathValue("platform"), r.PathValue("botname"))
		}))
	}

	if deps.HttpReceiver != nil {
		mux.Handle("POST /api/v1/channels/http/messages", wrap(SendHttpChannelMessage(deps.HttpReceiver)))
		mux.Handle("POST /api/v1/channels/http/chat", wrap(ChatWithHttpChannel(deps.HttpReceiver)))
	}
}
