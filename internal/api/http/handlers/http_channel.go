package handlers

import (
	"encoding/json"
	stdhttp "net/http"
	"strings"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	"github.com/benenen/myclaw/internal/channel/httpchan"
)

func SendHttpChannelMessage(receiver *httpchan.Receiver) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.HttpChannelMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if strings.TrimSpace(req.BotID) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.Text) == "" || strings.TrimSpace(req.CallbackURL) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "bot_id, user_id, text and callback_url are required")
			return
		}

		if !receiver.Active(req.BotID) {
			httpapi.WriteError(w, r, "NOT_FOUND", "bot is not active or not an http channel bot")
			return
		}

		if err := receiver.Receive(req.BotID, httpchan.IncomingMessage{
			UserID:      req.UserID,
			Text:        req.Text,
			MessageID:   req.MessageID,
			CallbackURL: req.CallbackURL,
		}); err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		httpapi.WriteOKFromRequest(w, r, map[string]string{
			"bot_id":     req.BotID,
			"message_id": req.MessageID,
			"status":     "accepted",
		})
	}
}
