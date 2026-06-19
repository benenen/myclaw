package feishu

import (
	"os"
	"strings"
)

// Config holds environment-level (not per-bot) feishu settings. Per-bot App
// ID/Secret are supplied at connect time, not here.
type Config struct {
	// Domain is the Feishu/Lark API base URL. Feishu: https://open.feishu.cn
	// Lark (international): https://open.larksuite.com
	Domain string
	// Trace enables the live tool-call trace card. Default true; disable with
	// CHANNEL_FEISHU_TRACE=0 (or false/off/no).
	Trace bool
}

func LoadConfig() Config {
	return Config{
		Domain: getEnvOrDefault("FEISHU_DOMAIN", "https://open.feishu.cn"),
		Trace:  envBoolDefaultTrue("CHANNEL_FEISHU_TRACE"),
	}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBoolDefaultTrue(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}
