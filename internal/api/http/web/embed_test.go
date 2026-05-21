package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedIndexLoadsCapabilitiesBeforeBots(t *testing.T) {
	body := readStaticFS(t, "app.js")
	if !strings.Contains(body, "loadAgentCapabilities().then(() => loadBots())") {
		t.Fatal("app.js missing agent capability bootstrap call")
	}
}

func TestHandlerServesStaticFiles(t *testing.T) {
	t.Run("index.html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)
		body := rec.Body.String()

		if !strings.HasPrefix(body, "<!DOCTYPE html>") {
			t.Fatal("response is not HTML")
		}
		if !strings.Contains(body, "<title>myclaw Bots</title>") {
			t.Fatal("missing title")
		}
		if !strings.Contains(body, "href=\"style.css\"") {
			t.Fatal("missing style.css link")
		}
		if !strings.Contains(body, "src=\"app.js\"") {
			t.Fatal("missing app.js script")
		}

		for _, want := range []string{"myclaw", "Bots", "Bot List", "New Bot", "Login / Connect", "Webhook"} {
			if !strings.Contains(body, want) {
				t.Fatalf("response missing %q", want)
			}
		}
		for _, id := range []string{"create-bot-capability", "create-bot-mode", "detail-agent-capability", "detail-agent-mode", "qr-modal", "qr-share-link", "qr-status-text", "detail-hook-url"} {
			if !strings.Contains(body, id) {
				t.Fatalf("response missing element id %q", id)
			}
		}
		for _, handler := range []string{"copyShareURL()", "saveSelectedBotAgent()", "closeQRModal()", "openCreateBotModal()", "connectSelectedBot()", "closeCreateBotModal()", "copyHookUrl()"} {
			if !strings.Contains(body, handler) {
				t.Fatalf("response missing onclick handler %q", handler)
			}
		}
		for _, unwanted := range []string{
			"myclaw / live channel runtime",
			"init · request · response",
			"Operate the full channel loop from a single cockpit",
			"ChannelInit boot the long connection / loop",
			"ChannelOnRequest capture inbound channel traffic",
			"ChannelResponse relay model output back downstream",
			"runtime posture",
			"Loop State",
			"console armed",
			"Ingress Path",
			"request watch",
			"Egress Path",
			"response relay",
			"01 · runtime boot",
			"02 · ingress handling",
			"03 · downstream relay",
		} {
			if strings.Contains(body, unwanted) {
				t.Fatalf("response still contains removed copy %q", unwanted)
			}
		}
	})

	t.Run("style.css", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, ":root") {
			t.Fatal("style.css missing :root")
		}
		if !strings.Contains(body, "--accent:") {
			t.Fatal("style.css missing accent variable")
		}
	})

	t.Run("app.js", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, "async function loadBots") {
			t.Fatal("app.js missing loadBots function")
		}
		if !strings.Contains(body, "async function api(") {
			t.Fatal("app.js missing api helper")
		}
		if !strings.Contains(body, "function hookUrl(botName)") {
			t.Fatal("app.js missing hookUrl function")
		}
		if !strings.Contains(body, "function copyHookUrl()") {
			t.Fatal("app.js missing copyHookUrl function")
		}
	})

	t.Run("404 on unknown path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})
}

func readStaticFS(t *testing.T, name string) string {
	t.Helper()
	f, err := staticFS.Open("static/" + name)
	if err != nil {
		t.Fatalf("open embedded %s: %v", name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(data)
}
