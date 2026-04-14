package handlers

import (
	"context"
	stdhttp "net/http"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	botapp "github.com/benenen/myclaw/internal/app/bot"
)

type MessageSimulator interface {
	Simulate(ctx context.Context, input botapp.SimulateMessageInput) (botapp.SimulateMessageOutput, error)
}

type Dependencies struct {
	BotService       *botapp.BotService
	MessageSimulator MessageSimulator
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
}
