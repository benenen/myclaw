package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedIndexSelectsBotBeforeRenderingList(t *testing.T) {
	html := string(indexHTML)
	start := strings.Index(html, "async function loadBots(preferBotId) {")
	if start == -1 {
		t.Fatalf("loadBots function not found")
	}
	body := html[start:]
	selectionIndex := strings.Index(body, "  if (preferBotId) {\n    selectedBotId = preferBotId;\n  } else if (!bots.some(b => b.bot_id === selectedBotId)) {\n    selectedBotId = bots[0]?.bot_id || '';\n  }")
	listIndex := strings.Index(body, "  renderBotList();")
	if selectionIndex == -1 {
		t.Fatalf("selection fallback snippet not found")
	}
	if listIndex == -1 {
		t.Fatalf("renderBotList call not found")
	}
	if selectionIndex > listIndex {
		t.Fatalf("selection fallback appears after renderBotList")
	}
}

func TestEmbeddedIndexLoadsCapabilitiesBeforeBots(t *testing.T) {
	html := string(indexHTML)
	capabilitiesIndex := strings.Index(html, "loadAgentCapabilities().then(() => loadBots());")
	if capabilitiesIndex == -1 {
		t.Fatalf("capability bootstrap call not found")
	}
}


func TestHandlerServesRuntimeConsoleBranding(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "Channel Plugin") {
		t.Fatalf("response still contains old product name: %q", body)
	}
	for _, want := range []string{"myclaw", "Bots", "Bot List", "New Bot", "Login / Connect", "qr-modal", "showQRModal(result.qr_code_payload, result.qr_share_url, result.status)", "copyShareURL()", "qr-share-link", "document.getElementById('connect-result').innerHTML = ''", "id=\"qr-status-text\"", "image.src = payload", "deleteBot(", "Delete bot", "agent-capabilities", "create-bot-capability", "create-bot-mode", "detail-agent-capability", "detail-agent-mode", "saveSelectedBotAgent()", "loadAgentCapabilities()", "supported_modes", "agent_capability_id", "agent_mode"} {
		if !strings.Contains(body, want) {
			t.Fatalf("response does not contain %q: %q", want, body)
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
			t.Fatalf("response still contains removed copy %q: %q", unwanted, body)
		}
	}
}
