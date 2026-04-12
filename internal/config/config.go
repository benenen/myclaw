package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

const requiredMasterKeyBytes = 32

var ErrMissingMasterKey = errors.New("CHANNEL_MASTER_KEY is required")

type Config struct {
	HTTPAddr         string
	SQLitePath       string
	LogLevel         string
	ChannelMasterKey []byte
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:   getEnvOrDefault("CHANNEL_HTTP_ADDR", ":8080"),
		SQLitePath: getEnvOrDefault("CHANNEL_SQLITE_PATH", "channel.db"),
		LogLevel:   getEnvOrDefault("LOG_LEVEL", "info"),
	}

	masterKeyB64 := os.Getenv("CHANNEL_MASTER_KEY")
	if masterKeyB64 == "" {
		return Config{}, ErrMissingMasterKey
	}

	masterKey, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return Config{}, fmt.Errorf("decode CHANNEL_MASTER_KEY: %w", err)
	}
	if len(masterKey) != requiredMasterKeyBytes {
		return Config{}, fmt.Errorf("CHANNEL_MASTER_KEY must decode to %d bytes, got %d", requiredMasterKeyBytes, len(masterKey))
	}

	cfg.ChannelMasterKey = masterKey
	return cfg, nil
}

func getEnvOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
