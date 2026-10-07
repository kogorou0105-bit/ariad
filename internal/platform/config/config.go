// Package config owns environment-backed process configuration.
package config

import (
	"os"
	"strconv"
)

const (
	defaultAPIAddress                   = ":8080"
	defaultLogLevel                     = "info"
	defaultConversationHistoryTurnLimit = 5
)

// Settings contains environment-backed configuration shared by the API and
// Worker. Callers must not log secret-bearing values such as DatabaseURL.
type Settings struct {
	APIAddress                   string
	LogLevel                     string
	DatabaseURL                  string
	AdminToken                   string
	ModelConfigEncryptionKey     string
	ConversationHistoryTurnLimit int
}

// Load reads the current process environment without retaining mutable global
// state, so API and Worker lifecycles remain independent.
func Load() Settings {
	return Settings{
		APIAddress:               valueOrDefault("ARIAD_API_ADDR", defaultAPIAddress),
		LogLevel:                 valueOrDefault("ARIAD_LOG_LEVEL", defaultLogLevel),
		DatabaseURL:              os.Getenv("ARIAD_DATABASE_URL"),
		AdminToken:               os.Getenv("ARIAD_ADMIN_TOKEN"),
		ModelConfigEncryptionKey: os.Getenv("ARIAD_MODEL_CONFIG_ENCRYPTION_KEY"),
		ConversationHistoryTurnLimit: positiveIntOrDefault(
			"ARIAD_CONVERSATION_HISTORY_TURN_LIMIT",
			defaultConversationHistoryTurnLimit,
		),
	}
}

func positiveIntOrDefault(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
