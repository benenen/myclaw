package handlers

import (
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"time"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/api/http/dto"
	"github.com/benenen/myclaw/internal/channel/httpchan"
	"github.com/benenen/myclaw/internal/domain"
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

func ChatWithHttpChannel(receiver *httpchan.Receiver) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var req dto.HttpChannelChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		if strings.TrimSpace(req.BotID) == "" || strings.TrimSpace(req.Text) == "" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "bot_id and text are required")
			return
		}

		if !receiver.Active(req.BotID) {
			httpapi.WriteError(w, r, "NOT_FOUND", "bot is not active or not an http channel bot")
			return
		}

		messageID := domain.NewPrefixedID("chat")
		replyCh, unregister := receiver.RegisterChat(req.BotID, messageID)
		defer unregister()

		if err := receiver.Receive(req.BotID, httpchan.IncomingMessage{
			UserID:    "webui",
			Text:      req.Text,
			MessageID: messageID,
		}); err != nil {
			httpapi.WriteError(w, r, "INTERNAL_ERROR", err.Error())
			return
		}

		select {
		case reply := <-replyCh:
			httpapi.WriteOKFromRequest(w, r, dto.HttpChannelChatResponse{
				BotID:     req.BotID,
				UserID:    "webui",
				Text:      reply.Text,
				MessageID: messageID,
			})
		case <-time.After(60 * time.Second):
			httpapi.WriteError(w, r, "TIMEOUT", "bot did not reply within 60 seconds")
		}
	}
}
