package config

import (
	"encoding/base64"
	"testing"
)

func TestLoadConfigUsesDefaults(t *testing.T) {
	t.Setenv("CHANNEL_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.SQLitePath != "channel.db" {
		t.Fatalf("SQLitePath = %q", cfg.SQLitePath)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q", cfg.LogLevel)
	}
	if len(cfg.ChannelMasterKey) != 32 {
		t.Fatalf("ChannelMasterKey length = %d", len(cfg.ChannelMasterKey))
	}
}

func TestLoadConfigRequiresMasterKey(t *testing.T) {
	t.Setenv("CHANNEL_MASTER_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadConfigRejectsInvalidMasterKeyLength(t *testing.T) {
	t.Setenv("CHANNEL_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("short")))

	_, err := Load()
	if err == nil {
		t.Fatal("expected invalid key length error")
	}
}
