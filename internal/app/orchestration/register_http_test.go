package orchestration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/benenen/myclaw/internal/domain"
)

func TestRegisterHandlerUpsertsRemoteAgent(t *testing.T) {
	reg := &recordingRegistry{}
	h := RegisterHandler(reg)

	req := httptest.NewRequest(http.MethodPost, "/a2a/register",
		strings.NewReader(`{"name":"weatherbot","description":"weather","endpoint":"http://x:9000/a2a"}`))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(reg.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(reg.upserts))
	}
	got := reg.upserts[0]
	if got.Kind != domain.RegisteredAgentKindRemote || got.Endpoint != "http://x:9000/a2a" || got.Health != domain.RegisteredAgentHealthy {
		t.Fatalf("unexpected upsert: %+v", got)
	}
	_ = context.Background()
}

func TestRegisterHandlerRejectsMissingFields(t *testing.T) {
	h := RegisterHandler(&recordingRegistry{})
	req := httptest.NewRequest(http.MethodPost, "/a2a/register", strings.NewReader(`{"name":""}`))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK { // envelope-style: still 200 but code != OK
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"code":"OK"`) {
		t.Fatalf("expected non-OK envelope, got %s", rec.Body.String())
	}
}
