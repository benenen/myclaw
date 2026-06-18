package feishu

import "os"

// Config holds environment-level (not per-bot) feishu settings. Per-bot App
// ID/Secret are supplied at connect time, not here.
type Config struct {
	// Domain is the Feishu/Lark API base URL. Feishu: https://open.feishu.cn
	// Lark (international): https://open.larksuite.com
	Domain string
}

func LoadConfig() Config {
	return Config{Domain: getEnvOrDefault("FEISHU_DOMAIN", "https://open.feishu.cn")}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
