package handlers

import (
	stdhttp "net/http"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/app"
)

type Dependencies struct {
	BotService *app.BotService
}

func RegisterRoutes(mux *stdhttp.ServeMux, deps Dependencies) {
	wrap := func(h stdhttp.HandlerFunc) stdhttp.Handler {
		return httpapi.RequestIDMiddleware()(h)
	}

	mux.Handle("POST /api/v1/bots/create", wrap(CreateBot(deps.BotService)))
	mux.Handle("GET /api/v1/bots/list", wrap(ListBots(deps.BotService)))
	mux.Handle("POST /api/v1/bots/connect", wrap(ConnectBot(deps.BotService)))
	mux.Handle("POST /api/v1/bots/delete", wrap(DeleteBot(deps.BotService)))
	mux.Handle("GET /api/v1/bots/connect", wrap(RefreshBotLogin(deps.BotService)))
}
