package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"

	"github.com/benenen/channel-plugin/internal/config"
)

func TestBootstrapBuildsDependencies(t *testing.T) {
	// Generate a valid master key
	key := make([]byte, 32)
	rand.Read(key)
	os.Setenv("CHANNEL_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	defer os.Unsetenv("CHANNEL_MASTER_KEY")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SQLitePath = ":memory:"

	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if app.Handler == nil {
		t.Fatal("expected handler")
	}
}
