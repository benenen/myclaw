package httpapi

import (
	"encoding/json"
	stdhttp "net/http"
)

type Envelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Data      any    `json:"data"`
}

func WriteOK(w stdhttp.ResponseWriter, requestID string, data any) {
	writeJSON(w, stdhttp.StatusOK, Envelope{
		Code:      "OK",
		Message:   "success",
		RequestID: requestID,
		Data:      data,
	})
}

func WriteOKFromRequest(w stdhttp.ResponseWriter, r *stdhttp.Request, data any) {
	WriteOK(w, RequestIDFromContext(r.Context()), data)
}

func WriteError(w stdhttp.ResponseWriter, r *stdhttp.Request, code string, message string) {
	writeJSON(w, stdhttp.StatusOK, Envelope{
		Code:      code,
		Message:   message,
		RequestID: RequestIDFromContext(r.Context()),
		Data:      map[string]any{},
	})
}

func writeJSON(w stdhttp.ResponseWriter, status int, body Envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
