package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ariad/internal/platform/config"
	"ariad/internal/platform/health"
	"ariad/internal/platform/observability"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	settings := config.Load()
	logger := observability.NewLogger(os.Stdout, settings.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, settings.APIAddress); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           newRouter(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveError := make(chan error, 1)
	go func() {
		logger.Info("api started", "address", address)
		serveError <- server.ListenAndServe()
	}()

	select {
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("api shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func newRouter() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)

	// Bounded request/response endpoints opt into a handler deadline. Streaming
	// routes such as SSE must be registered outside this group and manage their
	// own handshake, idle, and business-level deadlines.
	router.Group(func(bounded chi.Router) {
		bounded.Use(middleware.Timeout(30 * time.Second))
		bounded.Get("/healthz", health.Handler)
	})

	return router
}
