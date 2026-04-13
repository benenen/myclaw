package handlers

import (
	stdhttp "net/http"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	botapp "github.com/benenen/myclaw/internal/app/bot"
)

func ListAgentCapabilities(svc *botapp.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		items, err := svc.ListAgentCapabilities(r.Context())
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		resp := make([]dto.AgentCapabilityResponse, 0, len(items))
		for _, item := range items {
			resp = append(resp, dto.AgentCapabilityResponse{
				ID:             item.ID,
				Key:            item.Key,
				Label:          item.Label,
				SupportedModes: item.SupportedModes,
			})
		}
		httpapi.WriteOKFromRequest(w, r, resp)
	}
}
