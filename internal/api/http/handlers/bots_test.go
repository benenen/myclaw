package handlers

import (
	"encoding/json"
	stdhttp "net/http"
	"testing"

	httpapi "github.com/benenen/myclaw/internal/api/http"
	"github.com/benenen/myclaw/internal/app/bot"
	"github.com/benenen/myclaw/internal/channel/wechat"
	"github.com/benenen/myclaw/internal/security"
	"github.com/benenen/myclaw/internal/store/repositories"
	"github.com/benenen/myclaw/internal/testutil"
)

func newTestServer(t *testing.T) stdhttp.Handler {
	t.Helper()
	_, handler := newTestServerWithProvider(t)
	return handler
}

func newTestServerWithProvider(t *testing.T) (*wechat.FakeProvider, stdhttp.Handler) {
	t.Helper()
	db := testutil.OpenTestDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, _ := security.NewCipher(key)
	provider := wechat.NewFakeProvider()
	userRepo := repositories.NewUserRepository(db)
	bindingRepo := repositories.NewChannelBindingRepository(db)
	accountRepo := repositories.NewChannelAccountRepository(db)
	botRepo := repositories.NewBotRepository(db)
	mux := stdhttp.NewServeMux()
	RegisterRoutes(mux, Dependencies{
		BotService: bot.NewBotService(userRepo, botRepo, bindingRepo, accountRepo, repositories.NewAgentCapabilityRepository(db), cipher, provider, bot.NewBotConnectionManagerWithCipher(botRepo, accountRepo, provider, cipher, nil)),
	})
	return provider, mux
}

func TestCreateBotHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_123","name":"sales-bot","channel_type":"wechat","agent_capability_id":"cap_claude","agent_mode":"session"}`)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	var env httpapi.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "OK" {
		t.Fatalf("unexpected code: %s", env.Code)
	}
	payload, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	if payload["agent_capability_id"] != "cap_claude" {
		t.Fatalf("unexpected capability: %#v", payload["agent_capability_id"])
	}
	if payload["agent_mode"] != "session" {
		t.Fatalf("unexpected mode: %#v", payload["agent_mode"])
	}
}

func TestConfigureBotAgentHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	create := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_123","name":"sales-bot","channel_type":"wechat"}`)
	var env httpapi.Envelope
	if err := json.Unmarshal(create.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	payload, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	botID, ok := payload["bot_id"].(string)
	if !ok || botID == "" {
		t.Fatalf("unexpected bot id payload: %#v", payload["bot_id"])
	}

	rr := testutil.PostJSON(t, ts, "/api/v1/bots/agent", `{"bot_id":"`+botID+`","agent_capability_id":"cap_codex","agent_mode":"oneshot"}`)
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Code != "OK" {
		t.Fatalf("unexpected code: %s", env.Code)
	}
	payload, ok = env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	if payload["agent_capability_id"] != "cap_codex" {
		t.Fatalf("unexpected capability: %#v", payload["agent_capability_id"])
	}
	if payload["agent_mode"] != "oneshot" {
		t.Fatalf("unexpected mode: %#v", payload["agent_mode"])
	}
}

func TestListAgentCapabilitiesHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.GetJSON(t, ts, "/api/v1/agent-capabilities")
	testutil.AssertJSONCode(t, rr, "OK")
}

func TestCreateBotHandlerRejectsEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{}`)
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}

func TestListBotsHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	_ = testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_123","name":"sales-bot","channel_type":"wechat"}`)
	rr := testutil.GetJSON(t, ts, "/api/v1/bots/list?user_id=u_123")
	testutil.AssertJSONCode(t, rr, "OK")
}

func TestConnectBotHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	create := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_123","name":"sales-bot","channel_type":"wechat"}`)
	var env httpapi.Envelope
	if err := json.Unmarshal(create.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	payload, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	botID, ok := payload["bot_id"].(string)
	if !ok || botID == "" {
		t.Fatalf("unexpected bot id payload: %#v", payload["bot_id"])
	}
	rr := testutil.PostJSON(t, ts, "/api/v1/bots/connect", `{"bot_id":"`+botID+`"}`)
	testutil.AssertJSONCode(t, rr, "OK")
}

func TestConnectBotHandlerRejectsEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/bots/connect", `{}`)
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}

func TestRefreshBotLoginHandlerReturnsEnvelope(t *testing.T) {
	provider, ts := newTestServerWithProvider(t)
	create := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_123","name":"sales-bot","channel_type":"wechat"}`)
	var env httpapi.Envelope
	if err := json.Unmarshal(create.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	payload, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	botID, ok := payload["bot_id"].(string)
	if !ok || botID == "" {
		t.Fatalf("unexpected bot id payload: %#v", payload["bot_id"])
	}

	connect := testutil.PostJSON(t, ts, "/api/v1/bots/connect", `{"bot_id":"`+botID+`"}`)
	if err := json.Unmarshal(connect.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	payload, ok = env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	bindingID, ok := payload["binding_id"].(string)
	if !ok || bindingID == "" {
		t.Fatalf("unexpected binding id payload: %#v", payload["binding_id"])
	}
	providerBindingRef, ok := payload["binding_id"].(string)
	if !ok || providerBindingRef == "" {
		_ = providerBindingRef
	}
	provider.SimulateConfirm("wxbind_1")

	rr := testutil.GetJSON(t, ts, "/api/v1/bots/connect?binding_id="+bindingID)
	testutil.AssertJSONCode(t, rr, "OK")
}

func TestRefreshBotLoginHandlerRejectsEmptyBindingID(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.GetJSON(t, ts, "/api/v1/bots/connect")
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}

func TestRefreshBotLoginHandlerReturnsNotFoundForMissingBinding(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.GetJSON(t, ts, "/api/v1/bots/connect?binding_id=bind_missing")
	testutil.AssertJSONCode(t, rr, "NOT_FOUND")
}

func TestDeleteBotHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	create := testutil.PostJSON(t, ts, "/api/v1/bots/create", `{"user_id":"u_123","name":"sales-bot","channel_type":"wechat"}`)
	var env httpapi.Envelope
	if err := json.Unmarshal(create.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	payload, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type: %T", env.Data)
	}
	botID, ok := payload["bot_id"].(string)
	if !ok || botID == "" {
		t.Fatalf("unexpected bot id payload: %#v", payload["bot_id"])
	}

	rr := testutil.PostJSON(t, ts, "/api/v1/bots/delete", `{"bot_id":"`+botID+`"}`)
	testutil.AssertJSONCode(t, rr, "OK")

	list := testutil.GetJSON(t, ts, "/api/v1/bots/list?user_id=u_123")
	if err := json.Unmarshal(list.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	items, ok := env.Data.([]any)
	if !ok {
		t.Fatalf("unexpected list data type: %T", env.Data)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 bots, got %d", len(items))
	}
}

func TestDeleteBotHandlerRejectsEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/bots/delete", `{}`)
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}
