package handlers

import (
	stdhttp "net/http"
	"testing"

	"github.com/benenen/channel-plugin/internal/app"
	"github.com/benenen/channel-plugin/internal/channel/wechat"
	"github.com/benenen/channel-plugin/internal/security"
	"github.com/benenen/channel-plugin/internal/store/repositories"
	"github.com/benenen/channel-plugin/internal/testutil"
)

func newTestServer(t *testing.T) stdhttp.Handler {
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
	appKeyRepo := repositories.NewAppKeyRepository(db)

	mux := stdhttp.NewServeMux()
	RegisterRoutes(mux, Dependencies{
		BindingService:      app.NewBindingService(userRepo, bindingRepo, accountRepo, cipher, provider),
		AppKeyService:       app.NewAppKeyService(appKeyRepo, accountRepo),
		RuntimeService:      app.NewRuntimeService(appKeyRepo, accountRepo, cipher, provider),
		AccountQueryService: app.NewChannelAccountQueryService(userRepo, accountRepo, appKeyRepo),
	})
	return mux
}

func TestCreateBindingHandlerReturnsEnvelope(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/channel-bindings/create", `{"user_id":"u_123","channel_type":"wechat"}`)
	if rr.Code != stdhttp.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	testutil.AssertJSONCode(t, rr, "OK")
}

func TestCreateBindingHandlerRejectsEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/channel-bindings/create", `{}`)
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}

func TestBindingDetailNotFound(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.GetJSON(t, ts, "/api/v1/channel-bindings/detail?binding_id=bind_nonexist")
	testutil.AssertJSONCode(t, rr, "NOT_FOUND")
}

func TestDisableAppKeyRejectsBothIdentifiers(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.PostJSON(t, ts, "/api/v1/channel-accounts/app-key/disable", `{"channel_account_id":"acct_1","key_id":"key_1"}`)
	testutil.AssertJSONCode(t, rr, "INVALID_ARGUMENT")
}

func TestRuntimeConfigMissingAppKey(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.GetJSON(t, ts, "/api/v1/runtime/config")
	testutil.AssertJSONCode(t, rr, "APP_KEY_NOT_FOUND")
}

func TestRuntimeConfigUnknownAppKey(t *testing.T) {
	ts := newTestServer(t)
	rr := testutil.GetWithHeader(t, ts, "/api/v1/runtime/config", "X-App-Key", "appk_unknown_key_value")
	testutil.AssertJSONCode(t, rr, "APP_KEY_NOT_FOUND")
}
