package handlers

import (
	stdhttp "net/http"

	"github.com/benenen/channel-plugin/internal/app"
	httpapi "github.com/benenen/channel-plugin/internal/api/http"
)

type Dependencies struct {
	BindingService      *app.BindingService
	AppKeyService       *app.AppKeyService
	RuntimeService      *app.RuntimeService
	AccountQueryService *app.ChannelAccountQueryService
}

func RegisterRoutes(mux *stdhttp.ServeMux, deps Dependencies) {
	wrap := func(h stdhttp.HandlerFunc) stdhttp.Handler {
		return httpapi.RequestIDMiddleware()(h)
	}

	mux.Handle("POST /api/v1/channel-bindings/create", wrap(CreateBinding(deps.BindingService)))
	mux.Handle("GET /api/v1/channel-bindings/detail", wrap(GetBindingDetail(deps.BindingService)))
	mux.Handle("GET /api/v1/channel-accounts/list", wrap(ListChannelAccounts(deps.AccountQueryService)))
	mux.Handle("POST /api/v1/channel-accounts/app-key/create", wrap(CreateAppKey(deps.AppKeyService)))
	mux.Handle("POST /api/v1/channel-accounts/app-key/disable", wrap(DisableAppKey(deps.AppKeyService)))
	mux.Handle("GET /api/v1/runtime/config", wrap(GetRuntimeConfig(deps.RuntimeService)))
}
