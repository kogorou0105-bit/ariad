// Package config owns environment-backed process configuration.
package config

import "os"

const (
	defaultAPIAddress = ":8080"
	defaultLogLevel   = "info"
)

// Settings contains environment-backed configuration shared by the API and
// Worker. Callers must not log secret-bearing values such as DatabaseURL.
type Settings struct {
	APIAddress  string
	LogLevel    string
	DatabaseURL string
}

// Load reads the current process environment without retaining mutable global
// state, so API and Worker lifecycles remain independent.
func Load() Settings {
	return Settings{
		APIAddress:  valueOrDefault("ARIAD_API_ADDR", defaultAPIAddress),
		LogLevel:    valueOrDefault("ARIAD_LOG_LEVEL", defaultLogLevel),
		DatabaseURL: os.Getenv("ARIAD_DATABASE_URL"),
	}
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
