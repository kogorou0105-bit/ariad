package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ariad/internal/platform/config"
	"ariad/internal/platform/observability"
)

func main() {
	settings := config.Load()
	logger := observability.NewLogger(os.Stdout, settings.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	logger.Info("worker started")
	<-ctx.Done()
	logger.Info("worker shutdown requested")
	return nil
}
