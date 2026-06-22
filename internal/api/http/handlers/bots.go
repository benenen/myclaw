package handlers

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"strings"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	botapp "github.com/benenen/myclaw/internal/app/bot"
	"github.com/benenen/myclaw/internal/domain"
)

func CreateBot(svc *botapp.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.CreateBotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.UserID == "" || req.Name == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "user_id and name are required")
			return
		}

		result, err := svc.CreateBot(r.Context(), botapp.CreateBotInput{
			ExternalUserID:    req.UserID,
			Name:              req.Name,
			Type:              req.Type,
			Role:              req.Role,
			ChannelType:       req.ChannelType,
			AgentCapabilityID: req.AgentCapabilityID,
			AgentMode:         req.AgentMode,
			SystemPrompt:      req.SystemPrompt,
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
			BotID:             result.BotID,
			Name:              result.Name,
			Type:              result.Type,
			Role:              result.Role,
			ChannelType:       result.ChannelType,
			ConnectionStatus:  result.ConnectionStatus,
			ChannelAccountID:  result.ChannelAccountID,
			AgentCapabilityID: result.AgentCapabilityID,
			AgentMode:         result.AgentMode,
			SystemPrompt:      result.SystemPrompt,
		})
	}
}

func ListBots(svc *botapp.BotService) stdhttp.HandlerFunc {
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
				BotID:             item.BotID,
				Name:              item.Name,
				Type:              item.Type,
				Role:              item.Role,
				ChannelType:       item.ChannelType,
				ConnectionStatus:  item.ConnectionStatus,
				ChannelAccountID:  item.ChannelAccountID,
				AgentCapabilityID: item.AgentCapabilityID,
				AgentMode:         item.AgentMode,
				CLIAlias:          item.CLIAlias,
				MCPServerIDs:      item.MCPServerIDs,
				SystemPrompt:      item.SystemPrompt,
			})
		}
		httpapi.WriteOKFromRequest(w, r, resp)
	}
}

func ConfigureBotAgent(svc *botapp.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.ConfigureBotAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if req.BotID == "" || req.AgentCapabilityID == "" || req.AgentMode == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "bot_id, agent_capability_id and agent_mode are required")
			return
		}

		result, err := svc.ConfigureBotAgent(r.Context(), botapp.ConfigureBotAgentInput{
			BotID:             req.BotID,
			AgentCapabilityID: req.AgentCapabilityID,
			AgentMode:         req.AgentMode,
			CLIAlias:          req.CLIAlias,
			MCPServerIDs:      req.MCPServerIDs,
			SystemPrompt:      req.SystemPrompt,
		})
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

		httpapi.WriteOKFromRequest(w, r, dto.ConfigureBotAgentResponse{
			BotID:             result.BotID,
			Name:              result.Name,
			Type:              result.Type,
			ChannelType:       result.ChannelType,
			ConnectionStatus:  result.ConnectionStatus,
			ChannelAccountID:  result.ChannelAccountID,
			AgentCapabilityID: result.AgentCapabilityID,
			AgentMode:         result.AgentMode,
			Role:              result.Role,
			CLIAlias:          result.CLIAlias,
			MCPServerIDs:      result.MCPServerIDs,
			SystemPrompt:      result.SystemPrompt,
		})
	}
}

func ConnectBot(svc *botapp.BotService) stdhttp.HandlerFunc {
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

		input := botapp.StartBotLoginInput{BotID: req.BotID}
		if req.AppID != "" || req.AppSecret != "" {
			input.Config = map[string]string{
				"app_id":     req.AppID,
				"app_secret": req.AppSecret,
			}
		}
		result, err := svc.StartLogin(r.Context(), input)
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
			BotID:            result.BotID,
			BindingID:        result.BindingID,
			Status:           result.Status,
			QRCodePayload:    result.QRCodePayload,
			QRShareURL:       result.QRShareURL,
			ExpiresAt:        result.ExpiresAt,
			ConnectionStatus: result.ConnectionStatus,
			ChannelAccountID: result.ChannelAccountID,
		})
	}
}

func DeleteBot(svc *botapp.BotService) stdhttp.HandlerFunc {
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

func SimulateBotMessage(svc MessageSimulator) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.SimulateBotMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if strings.TrimSpace(req.BotID) == "" || strings.TrimSpace(req.From) == "" || strings.TrimSpace(req.RecipientID) == "" || strings.TrimSpace(req.Text) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "bot_id, from, recipient_id and text are required")
			return
		}
		if svc == nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", "message simulator is not configured")
			return
		}

		result, err := svc.Simulate(r.Context(), botapp.SimulateMessageInput{
			BotID:       req.BotID,
			From:        req.From,
			Text:        req.Text,
			MessageID:   req.MessageID,
			RecipientID: req.RecipientID,
		})
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

		httpapi.WriteOKFromRequest(w, r, dto.SimulateBotMessageResponse{
			BotID:       result.BotID,
			From:        result.From,
			Text:        result.Text,
			MessageID:   result.MessageID,
			RecipientID: result.RecipientID,
		})
	}
}

func ListMCPServers(svc *botapp.BotService) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		servers, err := svc.ListMCPServers(r.Context())
		if err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}
		out := make([]dto.MCPServerResponse, 0, len(servers))
		for _, s := range servers {
			out = append(out, dto.MCPServerResponse{ID: s.ID, Name: s.Name, ServerType: s.ServerType, Enabled: s.Enabled})
		}
		httpapi.WriteOKFromRequest(w, r, out)
	}
}

func RefreshBotLogin(svc *botapp.BotService) stdhttp.HandlerFunc {
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
