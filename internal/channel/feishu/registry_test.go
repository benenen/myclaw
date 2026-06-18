package feishu

import "testing"

func TestRegistryRegisterLookupUnregister(t *testing.T) {
	r := NewRegistry()

	if _, ok := r.Lookup("bot1"); ok {
		t.Fatal("expected no creds before register")
	}

	r.Register("bot1", Credentials{AppID: "cli_x", AppSecret: "s", BotOpenID: "ou_bot"})
	got, ok := r.Lookup("bot1")
	if !ok || got.AppID != "cli_x" || got.BotOpenID != "ou_bot" {
		t.Fatalf("Lookup after register = %#v, ok=%v", got, ok)
	}

	r.Unregister("bot1")
	if _, ok := r.Lookup("bot1"); ok {
		t.Fatal("expected no creds after unregister")
	}
}
