package handlers

import (
	"errors"
	stdhttp "net/http"

	"github.com/benenen/channel-plugin/internal/app"
	httpapi "github.com/benenen/channel-plugin/internal/api/http"
	"github.com/benenen/channel-plugin/internal/api/http/dto"
)

func GetRuntimeConfig(svc *app.RuntimeService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		appKey := r.Header.Get("X-App-Key")
		if appKey == "" {
			httpapi.WriteError(w, r, "APP_KEY_NOT_FOUND", "X-App-Key header is required")
			return
		}

		result, err := svc.GetByAppKey(r.Context(), appKey)
		if err != nil {
			if errors.Is(err, app.ErrAppKeyNotFound) {
				httpapi.WriteError(w, r, "APP_KEY_NOT_FOUND", "app key not found")
				return
			}
			if errors.Is(err, app.ErrAppKeyDisabled) {
				httpapi.WriteError(w, r, "APP_KEY_DISABLED", "app key is disabled")
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		resp := dto.RuntimeConfigResponse{
			ChannelType:      result.ChannelType,
			ChannelAccountID: result.ChannelAccountID,
			AccountUID:       result.AccountUID,
		}
		if blob, ok := result.RuntimeConfig["credential_blob"].(map[string]any); ok {
			resp.CredentialBlob = blob
		}
		if opts, ok := result.RuntimeConfig["runtime_options"].(map[string]any); ok {
			resp.RuntimeOptions = opts
		}

		httpapi.WriteOKFromRequest(w, r, resp)
	}
}
