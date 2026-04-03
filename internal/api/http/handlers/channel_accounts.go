package handlers

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"

	"github.com/benenen/channel-plugin/internal/app"
	httpapi "github.com/benenen/channel-plugin/internal/api/http"
	"github.com/benenen/channel-plugin/internal/api/http/dto"
	"github.com/benenen/channel-plugin/internal/domain"
)

func ListChannelAccounts(svc *app.ChannelAccountQueryService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "user_id is required")
			return
		}
		channelType := r.URL.Query().Get("channel_type")

		items, err := svc.ListByExternalUserID(r.Context(), userID, channelType)
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		result := make([]dto.ChannelAccountItem, len(items))
		for i, item := range items {
			result[i] = dto.ChannelAccountItem{
				ID:              item.ID,
				ChannelType:     item.ChannelType,
				AccountUID:      item.AccountUID,
				DisplayName:     item.DisplayName,
				AvatarURL:       item.AvatarURL,
				HasActiveAppKey: item.HasActiveAppKey,
				LastBoundAt:     item.LastBoundAt,
				CreatedAt:       item.CreatedAt,
			}
		}

		httpapi.WriteOKFromRequest(w, r, result)
	}
}

func CreateAppKey(svc *app.AppKeyService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.CreateAppKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.ChannelAccountID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "channel_account_id is required")
			return
		}

		result, err := svc.CreateOrRotate(r.Context(), req.ChannelAccountID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				httpapi.WriteError(w, r, "NOT_FOUND", "channel account not found")
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, dto.CreateAppKeyResponse{
			KeyID:        result.KeyID,
			AppKey:       result.AppKey,
			AppKeyPrefix: result.AppKeyPrefix,
			CreatedAt:    result.CreatedAt,
		})
	}
}

func DisableAppKey(svc *app.AppKeyService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.DisableAppKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}

		if req.ChannelAccountID != "" && req.KeyID != "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "provide either channel_account_id or key_id, not both")
			return
		}
		if req.ChannelAccountID == "" && req.KeyID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "provide either channel_account_id or key_id")
			return
		}

		err := svc.Disable(r.Context(), app.DisableAppKeyInput{
			ChannelAccountID: req.ChannelAccountID,
			KeyID:            req.KeyID,
		})
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, map[string]any{})
	}
}
