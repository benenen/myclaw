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

func CreateBinding(svc *app.BindingService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.CreateBindingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.UserID == "" || req.ChannelType == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "user_id and channel_type are required")
			return
		}

		result, err := svc.CreateBinding(r.Context(), app.CreateBindingInput{
			ExternalUserID: req.UserID,
			ChannelType:    req.ChannelType,
		})
		if err != nil {
			if errors.Is(err, domain.ErrInvalidArg) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", err.Error())
				return
			}
			httpapi.WriteError(w, r, "BINDING_FAILED", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, dto.CreateBindingResponse{
			BindingID:     result.BindingID,
			Status:        result.Status,
			QRCodePayload: result.QRCodePayload,
			ExpiresAt:     result.ExpiresAt,
		})
	}
}

func GetBindingDetail(svc *app.BindingService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		bindingID := r.URL.Query().Get("binding_id")
		if bindingID == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "binding_id is required")
			return
		}

		detail, err := svc.GetBindingDetail(r.Context(), bindingID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				httpapi.WriteError(w, r, "NOT_FOUND", "binding not found")
				return
			}
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		// Always return OK for existing bindings, even if status is failed/expired
		httpapi.WriteOKFromRequest(w, r, dto.BindingDetailResponse{
			BindingID:        detail.BindingID,
			Status:           detail.Status,
			ChannelType:      detail.ChannelType,
			ChannelAccountID: detail.ChannelAccountID,
			DisplayName:      detail.DisplayName,
			AccountUID:       detail.AccountUID,
			ExpiresAt:        detail.ExpiresAt,
			ErrorMessage:     detail.ErrorMessage,
		})
	}
}
