package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

func TestA2AClientMessageSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "message/send" {
			t.Errorf("unexpected method %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]any{
				"kind": "message",
				"role": "agent",
				"parts": []map[string]any{
					{"kind": "text", "text": "remote answer"},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewA2AClient(srv.Client())
	out, err := c.Run(context.Background(), domain.RegisteredAgent{
		Kind: domain.RegisteredAgentKindRemote, Endpoint: srv.URL,
	}, "do remote work")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "remote answer" {
		t.Fatalf("unexpected output %q", out)
	}
}
