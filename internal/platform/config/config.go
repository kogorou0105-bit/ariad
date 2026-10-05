// Package config owns environment-backed process configuration.
package config

import "os"

const (
	defaultAPIAddress = ":8080"
	defaultLogLevel   = "info"
)

// Settings contains configuration shared by the API and Worker. Validation and
// secret loading are added in the dedicated Foundation configuration task.
type Settings struct {
	APIAddress string
	LogLevel   string
}

// Load reads the current process environment without retaining mutable global
// state, so API and Worker lifecycles remain independent.
func Load() Settings {
	return Settings{
		APIAddress: valueOrDefault("ARIAD_API_ADDR", defaultAPIAddress),
		LogLevel:   valueOrDefault("ARIAD_LOG_LEVEL", defaultLogLevel),
	}
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
