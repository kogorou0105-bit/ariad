package main

import (
	"context"
	"log/slog"
	"testing"
)

func TestWorkerStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	logger := slog.New(slog.DiscardHandler)
	if err := run(ctx, logger); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}
