package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const requiredMasterKeyBytes = 32

var ErrMissingMasterKey = errors.New("CHANNEL_MASTER_KEY is required")

type Config struct {
	DataDir             string
	HTTPAddr            string
	SQLitePath          string
	LogLevel            string
	ChannelMasterKey    []byte
	OrchestratorTimeout time.Duration
	MCPURL              string
}

type DataPaths struct {
	DataDir    string
	SQLitePath string
}

func LoadDataPaths() (DataPaths, error) {
	dataDir, err := expandPath(getEnvOrDefault("CHANNEL_DATA_DIR", "~/.myclaw"))
	if err != nil {
		return DataPaths{}, err
	}

	sqlitePathEnv := os.Getenv("CHANNEL_SQLITE_PATH")
	sqlitePath := filepath.Join(dataDir, "myclaw.db")
	if sqlitePathEnv != "" {
		sqlitePath, err = expandPath(sqlitePathEnv)
		if err != nil {
			return DataPaths{}, err
		}
	}

	return DataPaths{
		DataDir:    dataDir,
		SQLitePath: sqlitePath,
	}, nil
}

func Load() (Config, error) {
	paths, err := LoadDataPaths()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		DataDir:    paths.DataDir,
		HTTPAddr:   getEnvOrDefault("CHANNEL_HTTP_ADDR", ":8080"),
		SQLitePath: paths.SQLitePath,
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

	cfg.OrchestratorTimeout = 30 * time.Minute
	if v := os.Getenv("CHANNEL_ORCHESTRATOR_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse CHANNEL_ORCHESTRATOR_TIMEOUT: %w", err)
		}
		cfg.OrchestratorTimeout = d
	}

	cfg.MCPURL = getEnvOrDefault("CHANNEL_MCP_URL", "http://127.0.0.1"+cfg.HTTPAddr+"/mcp")

	return cfg, nil
}

func (c Config) BotWorkspaceRoot() string {
	return filepath.Join(c.DataDir, "bots")
}

func (c Config) BotWorkspacePath(botID string) string {
	return filepath.Join(c.BotWorkspaceRoot(), botID, "workspace")
}

func getEnvOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func expandPath(path string) (string, error) {
	switch {
	case path == "":
		return "", nil
	case path == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return home, nil
	case strings.HasPrefix(path, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	default:
		return path, nil
	}
}
