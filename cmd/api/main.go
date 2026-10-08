package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ariad/internal/agent"
	"ariad/internal/auth"
	"ariad/internal/conversation"
	"ariad/internal/ingestion"
	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	"ariad/internal/platform/config"
	"ariad/internal/platform/database"
	platformfetch "ariad/internal/platform/fetch"
	"ariad/internal/platform/health"
	platformmodel "ariad/internal/platform/model"
	"ariad/internal/platform/observability"
	"ariad/internal/playground"
	"ariad/internal/retrieval"
	"ariad/internal/review"
	"ariad/internal/runtime"
	"ariad/internal/visitor"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	developmentWorkspaceID = "ws_dev"
	developmentAgentID     = "agent_dev"
	maximumKnowledgeBody   = 1 << 20
	maximumKnowledgeUpload = 50 << 20
	maximumIngestionBody   = 16 << 10
	maximumQuestionBody    = 64 << 10
	maximumModelConfigBody = 16 << 10
	maximumAuthBody        = 16 << 10
)

func main() {
	settings := config.Load()
	logger := observability.NewLogger(os.Stdout, settings.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, settings); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, settings config.Settings) error {
	modelAdapter, err := platformmodel.NewFromEnvironment()
	if err != nil {
		return fmt.Errorf("configure model adapter: %w", err)
	}
	handler, closePersistence, err := newConfiguredRouter(
		ctx,
		logger,
		modelAdapter,
		settings.DatabaseURL,
		settings.ConversationHistoryTurnLimit,
		settings.ModelConfigEncryptionKey,
	)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closePersistence(); closeErr != nil {
			logger.Error("close persistence", "error", closeErr)
		}
	}()
	server := &http.Server{
		Addr:              settings.APIAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveError := make(chan error, 1)
	go func() {
		logger.Info("api started", "address", settings.APIAddress)
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

func newRouter(logger *slog.Logger, modelAdapter runtime.Model) http.Handler {
	return newRouterWithBootstrapPassword(logger, modelAdapter, "")
}

func newRouterWithBootstrapPassword(logger *slog.Logger, modelAdapter runtime.Model, bootstrapPassword string) http.Handler {
	return newRouterWithRepositoriesAndBootstrapPassword(
		logger,
		modelAdapter,
		knowledge.NewMemoryRepository(),
		conversation.NewMemoryRepository(),
		platformfetch.NewHTTPFetcher(),
		0,
		bootstrapPassword,
	)
}

func newRouterWithRepositoriesAndBootstrapPassword(
	logger *slog.Logger,
	modelAdapter runtime.Model,
	knowledgeRepository knowledge.Repository,
	conversationRepository conversation.Repository,
	pageFetcher ingestion.Fetcher,
	historyTurnLimit int,
	bootstrapPassword string,
) http.Handler {
	return newRouterWithRepositoriesAndModelConfig(
		logger, modelAdapter, knowledgeRepository, conversationRepository, pageFetcher,
		historyTurnLimit, bootstrapPassword, modelconfig.NewMemoryRepository(), modelconfig.Config{},
	)
}

func newRouterWithRepositoriesAndModelConfig(
	logger *slog.Logger,
	modelAdapter runtime.Model,
	knowledgeRepository knowledge.Repository,
	conversationRepository conversation.Repository,
	pageFetcher ingestion.Fetcher,
	historyTurnLimit int,
	bootstrapPassword string,
	modelConfigRepository modelconfig.Repository,
	fallbackModelConfig modelconfig.Config,
) http.Handler {
	return newRouterWithAllRepositories(logger, modelAdapter, knowledgeRepository, conversationRepository, review.NewMemoryRepository(conversationRepository), pageFetcher, historyTurnLimit, bootstrapPassword, modelConfigRepository, fallbackModelConfig)
}

func newRouterWithAllRepositories(
	logger *slog.Logger, modelAdapter runtime.Model, knowledgeRepository knowledge.Repository,
	conversationRepository conversation.Repository, reviewRepository review.Repository,
	pageFetcher ingestion.Fetcher, historyTurnLimit int, bootstrapPassword string,
	modelConfigRepository modelconfig.Repository, fallbackModelConfig modelconfig.Config,
) http.Handler {
	authService := auth.NewService(auth.NewMemoryRepository(), auth.DefaultSessionTTL)
	if _, _, _, err := authService.Bootstrap(context.Background(), bootstrapPassword); err != nil {
		panic(err)
	}
	return newRouterWithAllServices(logger, modelAdapter, knowledgeRepository, conversationRepository, reviewRepository, pageFetcher, historyTurnLimit, modelConfigRepository, fallbackModelConfig, authService, visitor.NewService(visitor.NewMemoryRepository(), visitor.DefaultSessionTTL))
}

func newRouterWithAllServices(
	logger *slog.Logger, modelAdapter runtime.Model, knowledgeRepository knowledge.Repository,
	conversationRepository conversation.Repository, reviewRepository review.Repository,
	pageFetcher ingestion.Fetcher, historyTurnLimit int,
	modelConfigRepository modelconfig.Repository, fallbackModelConfig modelconfig.Config,
	authService *auth.Service,
	visitorService *visitor.Service,
) http.Handler {
	environmentEmbedder, embedderErr := platformmodel.NewEmbedderFromEnvironment()
	if embedderErr != nil {
		panic(fmt.Errorf("configure embedding adapter: %w", embedderErr))
	}
	modelConfigService := modelconfig.NewService(modelConfigRepository, fallbackModelConfig)
	embedder := platformmodel.NewWorkspaceEmbedder(modelConfigService, environmentEmbedder)
	knowledgeService := knowledge.NewService(
		knowledgeRepository,
		knowledge.WithEmbedder(embedder),
		knowledge.WithEmbeddingErrorHandler(func(workspaceID, chunkID string, err error) {
			logger.Warn("knowledge embedding failed; source retained for lexical retrieval", "workspace_id", workspaceID, "chunk_id", chunkID, "error", err)
		}),
	)
	ingestionService := ingestion.NewService(pageFetcher, knowledgeService)
	retrievalService := retrieval.NewService(knowledgeService, retrieval.WithEmbedder(embedder), retrieval.WithSemanticThresholdProvider(embedder), retrieval.WithEmbeddingErrorHandler(func(err error) { logger.Warn("semantic retrieval unavailable; lexical fallback used", "error", err) }))
	agentReader := agent.NewStaticReader([]agent.PublishedAgent{{
		WorkspaceID: developmentWorkspaceID,
		AgentID:     developmentAgentID,
		VersionID:   "agent_version_dev",
		Name:        "Ariad Support",
		Instructions: "Answer only from the supplied evidence. " +
			"Never invent facts and cite every factual answer.",
	}})
	reviewService := review.NewService(reviewRepository)
	workspaceModel := platformmodel.NewWorkspaceModel(modelConfigService, modelAdapter)
	runtimeService := runtime.NewService(agentReader, retrievalService, workspaceModel)
	playgroundService := playground.NewService(agentReader, knowledgeService, retrievalService, workspaceModel, modelConfigService)
	conversationService := conversation.NewService(
		conversationRepository,
		runtimeService,
		conversation.WithHistoryTurnLimit(historyTurnLimit),
		conversation.WithStateRecorder(reviewService),
	)
	// TODO(auth): replace this development-only scope with workspace and agent
	// identifiers derived from an authenticated token/session. Request-body IDs
	// are compatibility fields to validate, never the authority used downstream.
	scope := requestScope{workspaceID: developmentWorkspaceID, agentID: developmentAgentID}
	return newRouterWithServices(logger, scope, knowledgeService, ingestionService, conversationService, playgroundService, reviewService, modelConfigService, authService, visitorService)
}

func newConfiguredRouter(
	ctx context.Context,
	logger *slog.Logger,
	modelAdapter runtime.Model,
	databaseURL string,
	historyTurnLimit int,
	modelConfigEncryptionKey string,
) (http.Handler, func() error, error) {
	environmentModel := platformmodel.SettingsFromEnvironment()
	fallbackConfig := modelconfig.Config{
		BaseURL: environmentModel.BaseURL, Model: environmentModel.Model, APIKey: environmentModel.APIKey,
		EmbeddingBaseURL: environmentModel.BaseURL, EmbeddingModel: platformmodel.EmbeddingModelFromEnvironment(),
		EmbeddingAPIKey: environmentModel.APIKey, EmbeddingThreshold: 0.35,
	}
	if environmentModel.APIKey == "" {
		fallbackConfig = modelconfig.Config{Model: "grounded-v1"}
	}
	if databaseURL == "" {
		authService := auth.NewService(auth.NewMemoryRepository(), auth.DefaultSessionTTL)
		administrator, password, created, bootstrapErr := authService.Bootstrap(ctx, "")
		if bootstrapErr != nil {
			return nil, nil, bootstrapErr
		}
		logBootstrapAdministrator(logger, administrator, password, created)
		conversationRepository := conversation.NewMemoryRepository()
		return newRouterWithAllServices(logger, modelAdapter, knowledge.NewMemoryRepository(), conversationRepository, review.NewMemoryRepository(conversationRepository), platformfetch.NewHTTPFetcher(), historyTurnLimit, modelconfig.NewMemoryRepository(), fallbackConfig, authService, visitor.NewService(visitor.NewMemoryRepository(), visitor.DefaultSessionTTL)), func() error { return nil }, nil
	}
	postgres, err := database.Open(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect persistence: %w", err)
	}
	if err := database.VerifySchemaReady(ctx, postgres); err != nil {
		_ = postgres.Close()
		return nil, nil, err
	}
	modelConfigRepository, err := database.NewModelConfigRepository(postgres, modelConfigEncryptionKey)
	if err != nil {
		_ = postgres.Close()
		return nil, nil, err
	}
	authService := auth.NewService(database.NewAuthRepository(postgres), auth.DefaultSessionTTL)
	administrator, password, created, err := authService.Bootstrap(ctx, "")
	if err != nil {
		_ = postgres.Close()
		return nil, nil, fmt.Errorf("bootstrap administrator: %w", err)
	}
	logBootstrapAdministrator(logger, administrator, password, created)
	handler := newRouterWithAllServices(
		logger,
		modelAdapter,
		database.NewKnowledgeRepository(postgres),
		database.NewConversationRepository(postgres),
		database.NewReviewRepository(postgres),
		platformfetch.NewHTTPFetcher(),
		historyTurnLimit,
		modelConfigRepository,
		fallbackConfig,
		authService,
		visitor.NewService(database.NewVisitorRepository(postgres), visitor.DefaultSessionTTL),
	)
	return handler, postgres.Close, nil
}

func newRouterWithServices(
	logger *slog.Logger,
	scope requestScope,
	knowledgeService *knowledge.Service,
	ingestionService ingestion.URLSubmitter,
	conversationService conversation.QuestionService,
	playgroundService *playground.Service,
	reviewService *review.Service,
	modelConfigService *modelconfig.Service,
	authService *auth.Service,
	visitorService *visitor.Service,
) http.Handler {
	handlers := apiHandlers{
		logger:       logger,
		scope:        scope,
		knowledge:    knowledgeService,
		ingestion:    ingestionService,
		conversation: conversationService,
		playground:   playgroundService,
		review:       reviewService,
		modelConfig:  modelConfigService,
		auth:         authService,
		visitor:      visitorService,
	}
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	adminAuthentication := newAdminAuthentication(authService, logger)
	visitorAuthentication := visitorAuthentication{service: visitorService, logger: logger}

	// Bounded request/response endpoints opt into a handler deadline. Streaming
	// routes such as SSE must be registered outside this group and manage their
	// own handshake, idle, and business-level deadlines.
	router.Group(func(bounded chi.Router) {
		bounded.Use(middleware.Timeout(30 * time.Second))
		bounded.Get("/healthz", health.Handler)
		bounded.Post("/api/v1/visitor/sessions", handlers.createVisitorSession)
		bounded.Post("/api/v1/visitor/sessions/refresh", handlers.refreshVisitorSession)
		bounded.Get("/api/v1/conversations/{conversation_id}", handlers.listConversationTurns)
		bounded.Group(func(visitorRoutes chi.Router) {
			visitorRoutes.Use(visitorAuthentication.require)
			visitorRoutes.Post("/api/v1/questions", handlers.submitQuestion)
			visitorRoutes.Post("/api/v1/conversations/{conversation_id}/handoff", handlers.requestHandoff)
		})
		bounded.Post("/api/v1/admin/login", handlers.loginAdministrator)
		bounded.Group(func(management chi.Router) {
			management.Use(adminAuthentication.require)
			management.Post("/api/v1/admin/logout", handlers.logoutAdministrator)
			management.Post("/api/v1/admin/accounts", handlers.createAdministrator)
			management.Get("/api/v1/admin/accounts", handlers.listAdministrators)
			management.Delete("/api/v1/admin/accounts/{administrator_id}", handlers.deleteAdministrator)
			management.Post("/api/v1/admin/password", handlers.changeAdministratorPassword)
			management.Post("/api/v1/knowledge/text", handlers.submitKnowledge)
			management.Post("/api/v1/knowledge/files", handlers.uploadKnowledgeFiles)
			management.Get("/api/v1/knowledge/sources", handlers.listKnowledgeSources)
			management.Get("/api/v1/knowledge/sources/{source_id}", handlers.getKnowledgeSource)
			management.Delete("/api/v1/knowledge/sources/{source_id}", handlers.deleteKnowledgeSource)
			management.Post("/api/v1/knowledge/sources/{source_id}/reprocess", handlers.reprocessKnowledgeSource)
			management.Post("/api/v1/ingestion/url", handlers.submitURL)
			management.Post("/api/v1/playground/ask", handlers.askPlayground)
			management.Get("/api/v1/conversations", handlers.listConversations)
			management.Get("/api/v1/visitors", handlers.listVisitors)
			management.Get("/api/v1/visitors/{visitor_id}/conversations", handlers.listVisitorConversations)
			management.Get("/api/v1/reviews", handlers.listReviews)
			management.Post("/api/v1/reviews/{conversation_id}/replies", handlers.replyToReview)
			management.Post("/api/v1/reviews/{conversation_id}/resolve", handlers.resolveReview)
			management.Get("/api/v1/model-config", handlers.getModelConfig)
			management.Put("/api/v1/model-config", handlers.saveModelConfig)
			management.Delete("/api/v1/model-config", handlers.resetModelConfig)
			management.Post("/api/v1/knowledge/embeddings/backfill", handlers.startEmbeddingBackfill)
			management.Get("/api/v1/knowledge/embeddings/backfill", handlers.getEmbeddingBackfill)
		})
	})

	return router
}

type apiHandlers struct {
	logger       *slog.Logger
	scope        requestScope
	knowledge    *knowledge.Service
	ingestion    ingestion.URLSubmitter
	conversation conversation.QuestionService
	playground   *playground.Service
	review       *review.Service
	modelConfig  *modelconfig.Service
	auth         *auth.Service
	visitor      *visitor.Service
}

func logBootstrapAdministrator(logger *slog.Logger, administrator auth.Administrator, password string, created bool) {
	if created {
		logger.Warn("created initial administrator; save this password because it will not be shown again", "username", administrator.Username, "initial_password", password)
	}
}

type requestScope struct {
	workspaceID string
	agentID     string
}
