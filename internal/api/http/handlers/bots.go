package handlers

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	"github.com/benenen/myclaw/internal/app"
	"github.com/benenen/myclaw/internal/domain"
)

func CreateBot(svc *app.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.CreateBotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.UserID == "" || req.Name == "" || req.ChannelType == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "user_id, name and channel_type are required")
			return
		}

		result, err := svc.CreateBot(r.Context(), app.CreateBotInput{
			ExternalUserID: req.UserID,
			Name:           req.Name,
			ChannelType:    req.ChannelType,
		})
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, dto.CreateBotResponse{
			BotID:            result.BotID,
			Name:             result.Name,
			ChannelType:      result.ChannelType,
			ConnectionStatus: result.ConnectionStatus,
			ChannelAccountID: result.ChannelAccountID,
		})
	}
}

func ListBots(svc *app.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "user_id is required")
			return
		}

		items, err := svc.ListBots(r.Context(), userID)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		resp := make([]dto.BotResponse, 0, len(items))
		for _, item := range items {
			resp = append(resp, dto.BotResponse{
				BotID:            item.BotID,
				Name:             item.Name,
				ChannelType:      item.ChannelType,
				ConnectionStatus: item.ConnectionStatus,
				ChannelAccountID: item.ChannelAccountID,
			})
		}
		httpapi.WriteOKFromRequest(w, r, resp)
	}
}

func ConnectBot(svc *app.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.ConnectBotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.BotID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "bot_id is required")
			return
		}

		result, err := svc.StartLogin(r.Context(), app.StartBotLoginInput{BotID: req.BotID})
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			if errors.Is(err, domain.ErrNotFound) {
				httpapi.WriteError(w, r, "NOT_FOUND", err.Error())
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, dto.ConnectBotResponse{
			BotID:         result.BotID,
			BindingID:     result.BindingID,
			Status:        result.Status,
			QRCodePayload: result.QRCodePayload,
			QRShareURL:    result.QRShareURL,
			ExpiresAt:     result.ExpiresAt,
		})
	}
}

func DeleteBot(svc *app.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.DeleteBotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.BotID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "bot_id is required")
			return
		}

		err := svc.DeleteBot(r.Context(), req.BotID)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			if errors.Is(err, domain.ErrNotFound) {
				httpapi.WriteError(w, r, "NOT_FOUND", err.Error())
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, map[string]string{
			"bot_id": req.BotID,
		})
	}
}

func RefreshBotLogin(svc *app.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		bindingID := r.URL.Query().Get("binding_id")
		if bindingID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "binding_id is required")
			return
		}

		result, err := svc.RefreshLogin(r.Context(), bindingID)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			if errors.Is(err, domain.ErrNotFound) {
				httpapi.WriteError(w, r, "NOT_FOUND", err.Error())
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, dto.RefreshBotLoginResponse{
			BotID:            result.BotID,
			BindingID:        result.BindingID,
			Status:           result.Status,
			QRCodePayload:    result.QRCodePayload,
			QRShareURL:       result.QRShareURL,
			ExpiresAt:        result.ExpiresAt,
			ChannelAccountID: result.ChannelAccountID,
			ConnectionStatus: result.ConnectionStatus,
		})
	}
}
