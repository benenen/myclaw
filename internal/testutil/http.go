package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/benenen/channel-plugin/internal/api/http"
)

func PostJSON(t *testing.T, handler stdhttp.Handler, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := stdhttp.NewRequest(stdhttp.MethodPost, path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func GetJSON(t *testing.T, handler stdhttp.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := stdhttp.NewRequest(stdhttp.MethodGet, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func GetWithHeader(t *testing.T, handler stdhttp.Handler, path string, headerKey string, headerValue string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := stdhttp.NewRequest(stdhttp.MethodGet, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(headerKey, headerValue)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func AssertJSONCode(t *testing.T, rr *httptest.ResponseRecorder, expectedCode string) {
	t.Helper()
	body, _ := io.ReadAll(rr.Body)
	var env httpapi.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("failed to parse response: %s", body)
	}
	if env.Code != expectedCode {
		t.Fatalf("expected code %s, got %s (body: %s)", expectedCode, env.Code, body)
	}
}
