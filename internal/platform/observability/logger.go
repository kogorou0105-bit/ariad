// Package observability owns process-wide logging, metrics and tracing setup.
package observability

import (
	"io"
	"log/slog"
	"strings"
)

// NewLogger creates the structured JSON logger shared by process entry points.
func NewLogger(writer io.Writer, configuredLevel string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(configuredLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level}))
}
