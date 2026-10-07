package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"ariad/internal/retrieval"
	"ariad/internal/review"
	"ariad/internal/runtime"
	"ariad/internal/visitor"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func fileUploadIdempotencyKey(fileName string, data []byte) string {
	digest := sha256.New()
	extension := strings.ToLower(filepath.Ext(fileName))
	_, _ = fmt.Fprintf(digest, "%d:%s%d:", len(extension), extension, len(data))
	_, _ = digest.Write(data)
	return fmt.Sprintf("file:v1:%x", digest.Sum(nil))
}

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
	runtimeService := runtime.NewService(agentReader, retrievalService, platformmodel.NewWorkspaceModel(modelConfigService, modelAdapter))
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
	return newRouterWithServices(logger, scope, knowledgeService, ingestionService, conversationService, reviewService, modelConfigService, authService, visitorService)
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

type embeddingBackfillStatus struct {
	Status    string                     `json:"status"`
	Total     int                        `json:"total"`
	Completed int                        `json:"completed"`
	Failed    int                        `json:"failed"`
	Failures  []embeddingBackfillFailure `json:"failures,omitempty"`
	Error     string                     `json:"error,omitempty"`
	UpdatedAt *time.Time                 `json:"updated_at,omitempty"`
}

type embeddingBackfillFailure struct {
	ChunkID string `json:"chunk_id"`
	Reason  string `json:"reason"`
}

func (h apiHandlers) startEmbeddingBackfill(response http.ResponseWriter, request *http.Request) {
	requestID := middleware.GetReqID(request.Context())
	if request.URL.Query().Get("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	current, started, err := h.knowledge.StartEmbeddingBackfill(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not start embedding backfill", requestID)
		return
	}
	if !started {
		h.writeJSON(response, http.StatusAccepted, embeddingBackfillStatusFromDomain(current))
		return
	}
	go func() {
		if _, backfillErr := h.knowledge.BackfillEmbeddings(context.Background(), h.scope.workspaceID); backfillErr != nil {
			h.logger.Error("embedding backfill failed", "workspace_id", h.scope.workspaceID, "error", backfillErr)
		}
	}()
	h.writeJSON(response, http.StatusAccepted, embeddingBackfillStatusFromDomain(current))
}

func (h apiHandlers) getEmbeddingBackfill(response http.ResponseWriter, request *http.Request) {
	requestID := middleware.GetReqID(request.Context())
	if request.URL.Query().Get("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	status, found, err := h.knowledge.GetEmbeddingBackfill(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load embedding backfill", requestID)
		return
	}
	if !found {
		h.writeJSON(response, http.StatusOK, embeddingBackfillStatus{Status: "idle"})
		return
	}
	h.writeJSON(response, http.StatusOK, embeddingBackfillStatusFromDomain(status))
}

func embeddingBackfillStatusFromDomain(status knowledge.EmbeddingBackfillStatus) embeddingBackfillStatus {
	failures := make([]embeddingBackfillFailure, 0, len(status.Failures))
	for _, failure := range status.Failures {
		failures = append(failures, embeddingBackfillFailure{ChunkID: failure.ChunkID, Reason: failure.Reason})
	}
	var updatedAt *time.Time
	if !status.UpdatedAt.IsZero() {
		value := status.UpdatedAt
		updatedAt = &value
	}
	return embeddingBackfillStatus{Status: status.Status, Total: status.Total, Completed: status.Completed, Failed: status.Failed, Failures: failures, Error: status.Error, UpdatedAt: updatedAt}
}

type apiHandlers struct {
	logger       *slog.Logger
	scope        requestScope
	knowledge    *knowledge.Service
	ingestion    ingestion.URLSubmitter
	conversation conversation.QuestionService
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

type administratorCredentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type administratorResponse struct {
	AdministratorID string `json:"administrator_id"`
	Username        string `json:"username"`
}
type loginResponse struct {
	Token         string                `json:"token"`
	ExpiresAt     time.Time             `json:"expires_at"`
	Administrator administratorResponse `json:"administrator"`
}
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h apiHandlers) loginAdministrator(response http.ResponseWriter, request *http.Request) {
	var input administratorCredentialsRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	token, administrator, expiresAt, err := h.auth.Login(request.Context(), input.Username, input.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.writeError(response, http.StatusUnauthorized, "invalid_credentials", "invalid username or password", middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrTooManyAttempts) {
		h.writeError(response, http.StatusTooManyRequests, "login_rate_limited", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not create administrator session", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, Administrator: administratorResponse{AdministratorID: administrator.ID, Username: administrator.Username}})
}

func (h apiHandlers) listAdministrators(response http.ResponseWriter, request *http.Request) {
	administrators, err := h.auth.ListAdministrators(request.Context())
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not list administrators", middleware.GetReqID(request.Context()))
		return
	}
	items := make([]administratorResponse, 0, len(administrators))
	for _, administrator := range administrators {
		items = append(items, administratorResponse{AdministratorID: administrator.ID, Username: administrator.Username})
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"administrators": items})
}

func (h apiHandlers) changeAdministratorPassword(response http.ResponseWriter, request *http.Request) {
	var input changePasswordRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	administrator := authenticatedAdministrator(request)
	err := h.auth.ChangePassword(request.Context(), administrator.ID, input.CurrentPassword, input.NewPassword)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		h.writeError(response, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrPasswordTooShort) {
		h.writeError(response, http.StatusBadRequest, "invalid_password", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not change password", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"password_changed": true})
}

func (h apiHandlers) deleteAdministrator(response http.ResponseWriter, request *http.Request) {
	current := authenticatedAdministrator(request)
	err := h.auth.DeleteAdministrator(request.Context(), current.ID, chi.URLParam(request, "administrator_id"))
	if errors.Is(err, auth.ErrAdministratorNotFound) {
		h.writeError(response, http.StatusNotFound, "administrator_not_found", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrCannotDeleteSelf) || errors.Is(err, auth.ErrLastAdministrator) {
		h.writeError(response, http.StatusConflict, "administrator_not_deletable", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not delete administrator", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"deleted": true})
}

func (h apiHandlers) logoutAdministrator(response http.ResponseWriter, request *http.Request) {
	if err := h.auth.Logout(request.Context(), bearerToken(request)); err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not end administrator session", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"logged_out": true})
}

func (h apiHandlers) createAdministrator(response http.ResponseWriter, request *http.Request) {
	var input administratorCredentialsRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	administrator, err := h.auth.CreateAdministrator(request.Context(), input.Username, input.Password)
	if errors.Is(err, auth.ErrUsernameRequired) || errors.Is(err, auth.ErrPasswordTooShort) {
		h.writeError(response, http.StatusBadRequest, "invalid_administrator", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if errors.Is(err, auth.ErrUsernameExists) {
		h.writeError(response, http.StatusConflict, "administrator_exists", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not create administrator", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusCreated, administratorResponse{AdministratorID: administrator.ID, Username: administrator.Username})
}

func authenticatedAdministrator(request *http.Request) auth.Administrator {
	return administratorFromRequest(request)
}

type submitKnowledgeRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	RequestID      string `json:"request_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Title          string `json:"title"`
	Text           string `json:"text"`
}

type submitKnowledgeResponse struct {
	WorkspaceID string `json:"workspace_id"`
	RequestID   string `json:"request_id"`
	SourceID    string `json:"source_id"`
	ChunkCount  int    `json:"chunk_count"`
}

type submitURLRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	URL            string `json:"url"`
	Title          string `json:"title"`
	RequestID      string `json:"request_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h apiHandlers) submitKnowledge(response http.ResponseWriter, request *http.Request) {
	var input submitKnowledgeRequest
	if err := decodeJSON(response, request, maximumKnowledgeBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), input.RequestID)
		return
	}
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", input.RequestID)
		return
	}
	if input.RequestID == "" || input.IdempotencyKey == "" {
		h.writeError(
			response,
			http.StatusBadRequest,
			"invalid_request",
			"request_id and idempotency_key are required",
			input.RequestID,
		)
		return
	}

	result, err := h.knowledge.SubmitText(request.Context(), knowledge.SubmitTextCommand{
		WorkspaceID:    h.scope.workspaceID,
		RequestID:      input.RequestID,
		IdempotencyKey: input.IdempotencyKey,
		Title:          input.Title,
		Text:           input.Text,
	})
	if err != nil {
		if errors.Is(err, knowledge.ErrInvalidText) {
			h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), input.RequestID)
			return
		}
		if errors.Is(err, knowledge.ErrIdempotencyConflict) {
			h.writeError(response, http.StatusConflict, "idempotency_conflict", err.Error(), input.RequestID)
			return
		}
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not save knowledge", input.RequestID)
		return
	}
	h.writeJSON(response, http.StatusCreated, submitKnowledgeResponse{
		WorkspaceID: result.WorkspaceID,
		RequestID:   input.RequestID,
		SourceID:    result.SourceID,
		ChunkCount:  result.ChunkCount,
	})
}

type knowledgeSourceResponse struct {
	SourceID   string    `json:"source_id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	SourceURL  string    `json:"source_url,omitempty"`
	FileName   string    `json:"file_name,omitempty"`
	MediaType  string    `json:"media_type,omitempty"`
	FileSize   int64     `json:"file_size,omitempty"`
	ChunkCount int       `json:"chunk_count"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func sourceResponse(source knowledge.SourceSummary) knowledgeSourceResponse {
	return knowledgeSourceResponse{SourceID: source.ID, Type: source.Type, Title: source.Title, Status: source.Status, SourceURL: source.SourceURL, FileName: source.FileName, MediaType: source.MediaType, FileSize: source.FileSize, ChunkCount: source.ChunkCount, Error: source.Error, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
}

func (h apiHandlers) listKnowledgeSources(response http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", middleware.GetReqID(request.Context()))
		return
	}
	sources, err := h.knowledge.ListSources(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not list knowledge sources", middleware.GetReqID(request.Context()))
		return
	}
	items := make([]knowledgeSourceResponse, 0, len(sources))
	for _, source := range sources {
		items = append(items, sourceResponse(source))
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"workspace_id": h.scope.workspaceID, "sources": items})
}

func (h apiHandlers) getKnowledgeSource(response http.ResponseWriter, request *http.Request) {
	source, found, err := h.knowledge.GetSource(request.Context(), h.scope.workspaceID, chi.URLParam(request, "source_id"))
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load knowledge source", middleware.GetReqID(request.Context()))
		return
	}
	if !found {
		h.writeError(response, http.StatusNotFound, "not_found", "knowledge source not found", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, sourceResponse(source))
}

func (h apiHandlers) deleteKnowledgeSource(response http.ResponseWriter, request *http.Request) {
	deleted, err := h.knowledge.DeleteSource(request.Context(), h.scope.workspaceID, chi.URLParam(request, "source_id"))
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not delete knowledge source", middleware.GetReqID(request.Context()))
		return
	}
	if !deleted {
		h.writeError(response, http.StatusNotFound, "not_found", "knowledge source not found", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]bool{"deleted": true})
}

func (h apiHandlers) uploadKnowledgeFiles(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumKnowledgeUpload)
	if err := request.ParseMultipartForm(maximumKnowledgeUpload); err != nil {
		h.writeError(response, http.StatusRequestEntityTooLarge, "upload_too_large", "upload exceeds the 50 MiB request limit", middleware.GetReqID(request.Context()))
		return
	}
	if request.FormValue("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", middleware.GetReqID(request.Context()))
		return
	}
	files := request.MultipartForm.File["files"]
	if len(files) == 0 {
		h.writeError(response, http.StatusBadRequest, "invalid_request", "at least one file is required", middleware.GetReqID(request.Context()))
		return
	}
	results := make([]knowledgeSourceResponse, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			now := time.Now().UTC()
			results = append(results, knowledgeSourceResponse{Type: "file", Title: header.Filename, FileName: header.Filename, Status: "failed", Error: err.Error(), CreatedAt: now, UpdatedAt: now})
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, ingestion.MaximumFileSize+1))
		_ = file.Close()
		text, mediaType, parseErr := ingestion.ExtractFileContext(request.Context(), header.Filename, data)
		if readErr != nil {
			parseErr = readErr
		}
		errorMessage := ""
		if parseErr != nil {
			errorMessage = parseErr.Error()
		}
		result, err := h.knowledge.SubmitText(request.Context(), knowledge.SubmitTextCommand{WorkspaceID: h.scope.workspaceID, RequestID: middleware.GetReqID(request.Context()), IdempotencyKey: fileUploadIdempotencyKey(header.Filename, data), Title: header.Filename, Text: text, SourceType: "file", FileName: header.Filename, MediaType: mediaType, FileContent: data, ProcessingError: errorMessage})
		if err != nil {
			h.writeError(response, http.StatusInternalServerError, "internal_error", "could not save uploaded file", middleware.GetReqID(request.Context()))
			return
		}
		source, _, err := h.knowledge.GetSource(request.Context(), h.scope.workspaceID, result.SourceID)
		if err != nil {
			h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load uploaded file", middleware.GetReqID(request.Context()))
			return
		}
		results = append(results, sourceResponse(source))
	}
	h.writeJSON(response, http.StatusCreated, map[string]any{"sources": results})
}

func (h apiHandlers) reprocessKnowledgeSource(response http.ResponseWriter, request *http.Request) {
	summary, found, err := h.knowledge.GetSource(request.Context(), h.scope.workspaceID, chi.URLParam(request, "source_id"))
	if err != nil || !found {
		h.writeError(response, http.StatusNotFound, "not_found", "knowledge source not found", middleware.GetReqID(request.Context()))
		return
	}
	var text string
	switch summary.Type {
	case "file":
		text, _, err = ingestion.ExtractFileContext(request.Context(), summary.FileName, summary.FileContent)
	case "url":
		text, err = h.ingestion.FetchText(request.Context(), summary.SourceURL)
	default:
		h.writeError(response, http.StatusBadRequest, "invalid_request", "text sources cannot be reprocessed", middleware.GetReqID(request.Context()))
		return
	}
	if err == nil {
		err = h.knowledge.ReprocessSource(request.Context(), summary.Source, text)
	}
	if err != nil {
		h.writeError(response, http.StatusBadRequest, "processing_failed", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	updated, _, _ := h.knowledge.GetSource(request.Context(), h.scope.workspaceID, summary.ID)
	h.writeJSON(response, http.StatusOK, sourceResponse(updated))
}

func (h apiHandlers) submitURL(response http.ResponseWriter, request *http.Request) {
	var input submitURLRequest
	if err := decodeJSON(response, request, maximumIngestionBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), input.RequestID)
		return
	}
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", input.RequestID)
		return
	}
	if input.RequestID == "" || input.IdempotencyKey == "" {
		h.writeError(
			response,
			http.StatusBadRequest,
			"invalid_request",
			"request_id and idempotency_key are required",
			input.RequestID,
		)
		return
	}
	result, err := h.ingestion.SubmitURL(request.Context(), ingestion.SubmitURLCommand{
		WorkspaceID:    h.scope.workspaceID,
		URL:            input.URL,
		Title:          input.Title,
		RequestID:      input.RequestID,
		IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, ingestion.ErrInvalidURL),
			errors.Is(err, ingestion.ErrUnsafeURL),
			errors.Is(err, ingestion.ErrNoContent),
			errors.Is(err, knowledge.ErrInvalidText):
			h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), input.RequestID)
		case errors.Is(err, knowledge.ErrIdempotencyConflict):
			h.writeError(response, http.StatusConflict, "idempotency_conflict", err.Error(), input.RequestID)
		case errors.Is(err, platformfetch.ErrResponseTooLarge):
			h.writeError(
				response,
				http.StatusRequestEntityTooLarge,
				"response_too_large",
				platformfetch.ErrResponseTooLarge.Error(),
				input.RequestID,
			)
		case errors.Is(err, context.DeadlineExceeded):
			h.writeError(response, http.StatusGatewayTimeout, "fetch_timeout", "URL fetch timed out", input.RequestID)
		case errors.Is(err, platformfetch.ErrHTTPStatus):
			h.writeError(response, http.StatusBadGateway, "fetch_failed", err.Error(), input.RequestID)
		default:
			h.writeError(response, http.StatusInternalServerError, "internal_error", "could not ingest URL", input.RequestID)
		}
		return
	}
	h.writeJSON(response, http.StatusCreated, submitKnowledgeResponse{
		WorkspaceID: result.WorkspaceID,
		RequestID:   input.RequestID,
		SourceID:    result.SourceID,
		ChunkCount:  result.ChunkCount,
	})
}

type submitQuestionRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id"`
	Channel        string `json:"channel"`
	Locale         string `json:"locale"`
	RequestID      string `json:"request_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Question       string `json:"question"`
}

type createVisitorSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
}
type visitorSessionResponse struct {
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	VisitorID    string    `json:"visitor_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type refreshVisitorSessionRequest struct {
	WorkspaceID  string `json:"workspace_id"`
	RefreshToken string `json:"refresh_token"`
}
type visitorProfileResponse struct {
	VisitorID         string    `json:"visitor_id"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	ConversationCount int64     `json:"conversation_count"`
	TotalTurnCount    int64     `json:"total_turn_count"`
	LastActivityAt    time.Time `json:"last_activity_at"`
}

func (h apiHandlers) createVisitorSession(response http.ResponseWriter, request *http.Request) {
	var input createVisitorSessionRequest
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", middleware.GetReqID(request.Context()))
		return
	}
	token, refreshToken, session, err := h.visitor.Create(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not create visitor session", middleware.GetReqID(request.Context()))
		return
	}
	h.writeJSON(response, http.StatusCreated, visitorSessionResponse{Token: token, RefreshToken: refreshToken, VisitorID: session.Identity.VisitorID, ExpiresAt: session.ExpiresAt})
}

func (h apiHandlers) refreshVisitorSession(response http.ResponseWriter, request *http.Request) {
	var input refreshVisitorSessionRequest
	requestID := middleware.GetReqID(request.Context())
	if err := decodeJSON(response, request, maximumAuthBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), requestID)
		return
	}
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	token, refreshToken, session, err := h.visitor.Refresh(request.Context(), input.RefreshToken)
	if errors.Is(err, visitor.ErrInvalidSession) {
		h.writeError(response, http.StatusUnauthorized, "visitor_refresh_invalid", "visitor refresh credential is invalid", requestID)
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not refresh visitor session", requestID)
		return
	}
	h.writeJSON(response, http.StatusCreated, visitorSessionResponse{Token: token, RefreshToken: refreshToken, VisitorID: session.Identity.VisitorID, ExpiresAt: session.ExpiresAt})
}

type citationResponse struct {
	CitationID  string `json:"citation_id"`
	EvidenceID  string `json:"evidence_id"`
	SourceID    string `json:"source_id"`
	ChunkID     string `json:"chunk_id"`
	SourceTitle string `json:"source_title"`
	Quote       string `json:"quote"`
}

type submitQuestionResponse struct {
	WorkspaceID         string                      `json:"workspace_id"`
	AgentID             string                      `json:"agent_id"`
	ConversationID      string                      `json:"conversation_id"`
	MessageID           string                      `json:"message_id"`
	AnswerID            string                      `json:"answer_id"`
	RequestID           string                      `json:"request_id"`
	TerminalDisposition runtime.TerminalDisposition `json:"terminal_disposition"`
	Answer              string                      `json:"answer"`
	Citations           []citationResponse          `json:"citations"`
}

type conversationTurnResponse struct {
	MessageID           string                      `json:"message_id"`
	Message             string                      `json:"message"`
	MessageCreatedAt    time.Time                   `json:"message_created_at"`
	AnswerID            string                      `json:"answer_id"`
	TerminalDisposition runtime.TerminalDisposition `json:"terminal_disposition"`
	Answer              string                      `json:"answer"`
	AnswerCreatedAt     time.Time                   `json:"answer_created_at"`
	Citations           []citationResponse          `json:"citations"`
}

type listConversationTurnsResponse struct {
	WorkspaceID    string                     `json:"workspace_id"`
	ConversationID string                     `json:"conversation_id"`
	Status         string                     `json:"status"`
	HandoffReason  string                     `json:"handoff_reason,omitempty"`
	Turns          []conversationTurnResponse `json:"turns"`
	HumanReplies   []humanReplyResponse       `json:"human_replies"`
}

type humanReplyResponse struct {
	ReplyID   string    `json:"reply_id"`
	Source    string    `json:"source"`
	AuthorID  string    `json:"author_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type handoffRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Reason      string `json:"reason"`
}
type reviewMutationRequest struct {
	WorkspaceID string `json:"workspace_id"`
	VisitorID   string `json:"visitor_id"`
	Text        string `json:"text"`
}
type reviewItemResponse struct {
	ConversationID  string    `json:"conversation_id"`
	VisitorID       string    `json:"visitor_id"`
	Reason          string    `json:"reason"`
	RequestedBy     string    `json:"requested_by"`
	RequestedAt     time.Time `json:"requested_at"`
	LastActivityAt  time.Time `json:"last_activity_at"`
	LastMessageText string    `json:"last_message_text"`
}
type listReviewsResponse struct {
	WorkspaceID string               `json:"workspace_id"`
	Reviews     []reviewItemResponse `json:"reviews"`
}

type conversationSummaryResponse struct {
	ConversationID  string    `json:"conversation_id"`
	VisitorID       string    `json:"visitor_id"`
	MessageCount    int64     `json:"message_count"`
	StartedAt       time.Time `json:"started_at"`
	LastActivityAt  time.Time `json:"last_activity_at"`
	LastMessageText string    `json:"last_message_text"`
	Status          string    `json:"status"`
}

type listConversationsResponse struct {
	WorkspaceID   string                        `json:"workspace_id"`
	Conversations []conversationSummaryResponse `json:"conversations"`
}

type modelConfigRequest struct {
	WorkspaceID        string  `json:"workspace_id"`
	BaseURL            string  `json:"base_url"`
	Model              string  `json:"model"`
	APIKey             string  `json:"api_key"`
	EmbeddingBaseURL   string  `json:"embedding_base_url"`
	EmbeddingModel     string  `json:"embedding_model"`
	EmbeddingAPIKey    string  `json:"embedding_api_key"`
	EmbeddingThreshold float64 `json:"embedding_threshold"`
}

type modelConfigResponse struct {
	WorkspaceID         string                        `json:"workspace_id"`
	Source              string                        `json:"source"`
	BaseURL             string                        `json:"base_url"`
	Model               string                        `json:"model"`
	APIKeyMask          string                        `json:"api_key_mask"`
	EmbeddingBaseURL    string                        `json:"embedding_base_url"`
	EmbeddingModel      string                        `json:"embedding_model"`
	EmbeddingAPIKeyMask string                        `json:"embedding_api_key_mask"`
	EmbeddingThreshold  float64                       `json:"embedding_threshold"`
	SemanticEnabled     bool                          `json:"semantic_enabled"`
	EmbeddingHealth     platformmodel.EmbeddingHealth `json:"embedding_health"`
}

func (h apiHandlers) submitQuestion(response http.ResponseWriter, request *http.Request) {
	var input submitQuestionRequest
	if err := decodeJSON(response, request, maximumQuestionBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), input.RequestID)
		return
	}
	if input.WorkspaceID != h.scope.workspaceID || input.AgentID != h.scope.agentID {
		h.writeError(response, http.StatusForbidden, "context_not_allowed", "unknown workspace or agent", input.RequestID)
		return
	}
	if input.Channel != string(conversation.ChannelWidget) {
		h.writeError(response, http.StatusBadRequest, "invalid_request", "channel must be widget", input.RequestID)
		return
	}
	if input.RequestID == "" || input.IdempotencyKey == "" {
		h.writeError(
			response,
			http.StatusBadRequest,
			"invalid_request",
			"request_id and idempotency_key are required",
			input.RequestID,
		)
		return
	}
	if strings.TrimSpace(input.Question) == "" {
		h.writeError(response, http.StatusBadRequest, "invalid_request", "question is required", input.RequestID)
		return
	}

	turn, err := h.conversation.SubmitQuestion(request.Context(), conversation.SubmitQuestionCommand{
		WorkspaceID:    h.scope.workspaceID,
		AgentID:        h.scope.agentID,
		ConversationID: input.ConversationID,
		VisitorID:      visitorFromRequest(request).VisitorID,
		Channel:        conversation.Channel(input.Channel),
		Locale:         input.Locale,
		RequestID:      input.RequestID,
		IdempotencyKey: input.IdempotencyKey,
		Question:       input.Question,
	})
	if err != nil {
		if errors.Is(err, conversation.ErrConversationNotFound) {
			h.writeError(response, http.StatusNotFound, "conversation_not_found", err.Error(), input.RequestID)
			return
		}
		if errors.Is(err, conversation.ErrIdempotencyConflict) {
			h.writeError(response, http.StatusConflict, "idempotency_conflict", err.Error(), input.RequestID)
			return
		}
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not answer question", input.RequestID)
		return
	}

	h.writeJSON(response, http.StatusOK, submitQuestionResponse{
		WorkspaceID:         turn.Answer.WorkspaceID,
		AgentID:             turn.Answer.AgentID,
		ConversationID:      turn.Answer.ConversationID,
		MessageID:           turn.Message.ID,
		AnswerID:            turn.Answer.ID,
		RequestID:           input.RequestID,
		TerminalDisposition: turn.Answer.TerminalDisposition,
		Answer:              turn.Answer.Text,
		Citations:           citationResponses(turn.Answer.Citations),
	})
}

func (h apiHandlers) listConversationTurns(response http.ResponseWriter, request *http.Request) {
	workspaceID := request.URL.Query().Get("workspace_id")
	requestID := middleware.GetReqID(request.Context())
	if workspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	conversationID := chi.URLParam(request, "conversation_id")
	visitorID := request.URL.Query().Get("visitor_id")
	if _, adminErr := h.auth.Authenticate(request.Context(), bearerToken(request)); adminErr != nil {
		identity, visitorErr := h.visitor.Authenticate(request.Context(), bearerToken(request))
		if visitorErr != nil {
			code := "visitor_session_invalid"
			message := "visitor session is invalid"
			if errors.Is(visitorErr, visitor.ErrExpiredSession) {
				code, message = "visitor_session_expired", "visitor session has expired"
			}
			h.writeError(response, http.StatusUnauthorized, code, message, requestID)
			return
		}
		visitorID = identity.VisitorID
	}
	if visitorID == "" {
		h.writeError(response, http.StatusBadRequest, "invalid_request", "visitor_id is required for administrator access", requestID)
		return
	}
	turns, err := h.conversation.ListTurns(
		request.Context(),
		h.scope.workspaceID,
		conversationID,
		visitorID,
	)
	if err != nil {
		h.writeError(
			response,
			http.StatusInternalServerError,
			"internal_error",
			"could not load conversation",
			requestID,
		)
		return
	}

	turnResponses := make([]conversationTurnResponse, 0, len(turns))
	for _, turn := range turns {
		turnResponses = append(turnResponses, conversationTurnResponse{
			MessageID:           turn.Message.ID,
			Message:             turn.Message.Text,
			MessageCreatedAt:    turn.Message.CreatedAt,
			AnswerID:            turn.Answer.ID,
			TerminalDisposition: turn.Answer.TerminalDisposition,
			Answer:              turn.Answer.Text,
			AnswerCreatedAt:     turn.Answer.CreatedAt,
			Citations:           citationResponses(turn.Answer.Citations),
		})
	}
	status := string(review.StatusOngoing)
	reason := ""
	humanReplies := []humanReplyResponse{}
	if len(turns) > 0 {
		state, replies, reviewErr := h.review.Get(request.Context(), h.scope.workspaceID, conversationID, visitorID)
		if reviewErr != nil {
			h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load conversation state", requestID)
			return
		}
		status, reason = string(state.Status), state.Reason
		for _, reply := range replies {
			humanReplies = append(humanReplies, humanReplyResponse{ReplyID: reply.ID, Source: "human", AuthorID: reply.AuthorID, Text: reply.Text, CreatedAt: reply.CreatedAt})
		}
	}
	h.writeJSON(response, http.StatusOK, listConversationTurnsResponse{
		WorkspaceID:    h.scope.workspaceID,
		ConversationID: conversationID,
		Status:         status,
		HandoffReason:  reason,
		Turns:          turnResponses,
		HumanReplies:   humanReplies,
	})
}

func (h apiHandlers) requestHandoff(response http.ResponseWriter, request *http.Request) {
	var input handoffRequest
	if err := decodeJSON(response, request, maximumQuestionBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	requestID := middleware.GetReqID(request.Context())
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	state, err := h.review.Request(request.Context(), h.scope.workspaceID, chi.URLParam(request, "conversation_id"), visitorFromRequest(request).VisitorID, input.Reason, "visitor")
	if errors.Is(err, review.ErrConversationNotFound) {
		h.writeError(response, http.StatusNotFound, "conversation_not_found", err.Error(), requestID)
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not request human handoff", requestID)
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"conversation_id": state.ConversationID, "status": state.Status, "handoff_reason": state.Reason})
}

func (h apiHandlers) listReviews(response http.ResponseWriter, request *http.Request) {
	requestID := middleware.GetReqID(request.Context())
	if request.URL.Query().Get("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	items, err := h.review.ListPending(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load review queue", requestID)
		return
	}
	result := make([]reviewItemResponse, 0, len(items))
	for _, item := range items {
		result = append(result, reviewItemResponse{ConversationID: item.ConversationID, VisitorID: item.VisitorID, Reason: item.Reason, RequestedBy: item.RequestedBy, RequestedAt: item.RequestedAt, LastActivityAt: item.LastActivityAt, LastMessageText: item.LastMessageText})
	}
	h.writeJSON(response, http.StatusOK, listReviewsResponse{WorkspaceID: h.scope.workspaceID, Reviews: result})
}

func (h apiHandlers) replyToReview(response http.ResponseWriter, request *http.Request) {
	var input reviewMutationRequest
	if err := decodeJSON(response, request, maximumQuestionBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	requestID := middleware.GetReqID(request.Context())
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	reply, err := h.review.Reply(request.Context(), h.scope.workspaceID, chi.URLParam(request, "conversation_id"), input.VisitorID, authenticatedAdministrator(request).ID, input.Text)
	if errors.Is(err, review.ErrConversationNotFound) {
		h.writeError(response, http.StatusNotFound, "conversation_not_found", err.Error(), requestID)
		return
	}
	if errors.Is(err, review.ErrNotPending) {
		h.writeError(response, http.StatusConflict, "review_not_pending", err.Error(), requestID)
		return
	}
	if errors.Is(err, review.ErrReplyRequired) {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), requestID)
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not save human reply", requestID)
		return
	}
	h.writeJSON(response, http.StatusCreated, humanReplyResponse{ReplyID: reply.ID, Source: "human", AuthorID: reply.AuthorID, Text: reply.Text, CreatedAt: reply.CreatedAt})
}

func (h apiHandlers) resolveReview(response http.ResponseWriter, request *http.Request) {
	var input reviewMutationRequest
	if err := decodeJSON(response, request, maximumQuestionBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	requestID := middleware.GetReqID(request.Context())
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	conversationID := chi.URLParam(request, "conversation_id")
	err := h.review.Resolve(request.Context(), h.scope.workspaceID, conversationID, input.VisitorID, authenticatedAdministrator(request).ID)
	if errors.Is(err, review.ErrConversationNotFound) {
		h.writeError(response, http.StatusNotFound, "conversation_not_found", err.Error(), requestID)
		return
	}
	if errors.Is(err, review.ErrNotPending) {
		h.writeError(response, http.StatusConflict, "review_not_pending", err.Error(), requestID)
		return
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not resolve review", requestID)
		return
	}
	h.writeJSON(response, http.StatusOK, map[string]string{"conversation_id": conversationID, "status": string(review.StatusResolved)})
}

func (h apiHandlers) listConversations(response http.ResponseWriter, request *http.Request) {
	workspaceID := request.URL.Query().Get("workspace_id")
	requestID := middleware.GetReqID(request.Context())
	if workspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	visitorID := request.URL.Query().Get("visitor_id")
	var summaries []conversation.Summary
	var err error
	if visitorID == "" {
		summaries, err = h.conversation.ListConversations(request.Context(), h.scope.workspaceID)
	} else {
		summaries, err = h.conversation.ListVisitorConversations(request.Context(), h.scope.workspaceID, visitorID)
	}
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load conversations", requestID)
		return
	}
	items := make([]conversationSummaryResponse, 0, len(summaries))
	for _, summary := range summaries {
		status := summary.Status
		if status == "" || status == string(review.StatusOngoing) {
			state, _, stateErr := h.review.Get(request.Context(), h.scope.workspaceID, summary.ConversationID, summary.VisitorID)
			if stateErr != nil {
				h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load conversation status", requestID)
				return
			}
			status = string(state.Status)
		}
		items = append(items, conversationSummaryResponse{
			ConversationID:  summary.ConversationID,
			VisitorID:       summary.VisitorID,
			MessageCount:    summary.MessageCount,
			StartedAt:       summary.StartedAt,
			LastActivityAt:  summary.LastActivityAt,
			LastMessageText: summary.LastMessageText,
			Status:          status,
		})
	}
	h.writeJSON(response, http.StatusOK, listConversationsResponse{
		WorkspaceID: h.scope.workspaceID, Conversations: items,
	})
}

func (h apiHandlers) listVisitors(response http.ResponseWriter, request *http.Request) {
	requestID := middleware.GetReqID(request.Context())
	if request.URL.Query().Get("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	profiles, err := h.visitor.ListProfiles(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load visitors", requestID)
		return
	}
	conversations, err := h.conversation.ListConversations(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load visitor conversations", requestID)
		return
	}
	conversationCounts := make(map[string]int64)
	turnCounts := make(map[string]int64)
	lastActivity := make(map[string]time.Time)
	for _, summary := range conversations {
		conversationCounts[summary.VisitorID]++
		turnCounts[summary.VisitorID] += summary.MessageCount
		if summary.LastActivityAt.After(lastActivity[summary.VisitorID]) {
			lastActivity[summary.VisitorID] = summary.LastActivityAt
		}
	}
	items := make([]visitorProfileResponse, 0, len(profiles))
	for _, profile := range profiles {
		profile.ConversationCount = conversationCounts[profile.VisitorID]
		profile.TotalTurnCount = turnCounts[profile.VisitorID]
		if lastActivity[profile.VisitorID].After(profile.LastActivityAt) {
			profile.LastActivityAt = lastActivity[profile.VisitorID]
		}
		items = append(items, visitorProfileResponse{VisitorID: profile.VisitorID, FirstSeenAt: profile.FirstSeenAt, ConversationCount: profile.ConversationCount, TotalTurnCount: profile.TotalTurnCount, LastActivityAt: profile.LastActivityAt})
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"workspace_id": h.scope.workspaceID, "visitors": items})
}

func (h apiHandlers) listVisitorConversations(response http.ResponseWriter, request *http.Request) {
	requestID := middleware.GetReqID(request.Context())
	if request.URL.Query().Get("workspace_id") != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	summaries, err := h.conversation.ListVisitorConversations(request.Context(), h.scope.workspaceID, chi.URLParam(request, "visitor_id"))
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load visitor conversations", requestID)
		return
	}
	items := make([]conversationSummaryResponse, 0, len(summaries))
	for _, summary := range summaries {
		status := summary.Status
		if status == "" || status == string(review.StatusOngoing) {
			state, _, stateErr := h.review.Get(request.Context(), h.scope.workspaceID, summary.ConversationID, summary.VisitorID)
			if stateErr != nil {
				h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load conversation status", requestID)
				return
			}
			status = string(state.Status)
		}
		items = append(items, conversationSummaryResponse{ConversationID: summary.ConversationID, VisitorID: summary.VisitorID, MessageCount: summary.MessageCount, StartedAt: summary.StartedAt, LastActivityAt: summary.LastActivityAt, LastMessageText: summary.LastMessageText, Status: status})
	}
	h.writeJSON(response, http.StatusOK, listConversationsResponse{WorkspaceID: h.scope.workspaceID, Conversations: items})
}

func (h apiHandlers) getModelConfig(response http.ResponseWriter, request *http.Request) {
	workspaceID := request.URL.Query().Get("workspace_id")
	requestID := middleware.GetReqID(request.Context())
	if workspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	status, err := h.modelConfig.GetStatus(request.Context(), h.scope.workspaceID)
	if err != nil {
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not load model configuration", requestID)
		return
	}
	h.writeJSON(response, http.StatusOK, modelConfigResponseFromStatus(status))
}

func (h apiHandlers) saveModelConfig(response http.ResponseWriter, request *http.Request) {
	var input modelConfigRequest
	if err := decodeJSON(response, request, maximumModelConfigBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	requestID := middleware.GetReqID(request.Context())
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	status, err := h.modelConfig.Save(request.Context(), modelconfig.SaveCommand{
		WorkspaceID: h.scope.workspaceID, BaseURL: input.BaseURL, Model: input.Model, APIKey: input.APIKey, EmbeddingBaseURL: input.EmbeddingBaseURL, EmbeddingModel: input.EmbeddingModel, EmbeddingAPIKey: input.EmbeddingAPIKey, EmbeddingThreshold: input.EmbeddingThreshold,
	})
	if err != nil {
		switch {
		case errors.Is(err, modelconfig.ErrInvalidBaseURL), errors.Is(err, modelconfig.ErrModelRequired), errors.Is(err, modelconfig.ErrAPIKeyRequired):
			h.writeError(response, http.StatusBadRequest, "invalid_model_config", err.Error(), requestID)
		case errors.Is(err, modelconfig.ErrEncryptionDisabled):
			h.writeError(response, http.StatusServiceUnavailable, "model_config_unavailable", modelconfig.ErrEncryptionDisabled.Error(), requestID)
		default:
			h.writeError(response, http.StatusInternalServerError, "internal_error", "could not save model configuration", requestID)
		}
		return
	}
	h.writeJSON(response, http.StatusOK, modelConfigResponseFromStatus(status))
}

func (h apiHandlers) resetModelConfig(response http.ResponseWriter, request *http.Request) {
	workspaceID := request.URL.Query().Get("workspace_id")
	requestID := middleware.GetReqID(request.Context())
	if workspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", requestID)
		return
	}
	status, err := h.modelConfig.Reset(request.Context(), h.scope.workspaceID)
	if err != nil {
		if errors.Is(err, modelconfig.ErrEncryptionDisabled) {
			h.writeError(response, http.StatusServiceUnavailable, "model_config_unavailable", modelconfig.ErrEncryptionDisabled.Error(), requestID)
			return
		}
		h.writeError(response, http.StatusInternalServerError, "internal_error", "could not reset model configuration", requestID)
		return
	}
	h.writeJSON(response, http.StatusOK, modelConfigResponseFromStatus(status))
}

func modelConfigResponseFromStatus(status modelconfig.Status) modelConfigResponse {
	return modelConfigResponse{WorkspaceID: status.WorkspaceID, Source: status.Source, BaseURL: status.BaseURL, Model: status.Model, APIKeyMask: status.APIKeyMask, EmbeddingBaseURL: status.EmbeddingBaseURL, EmbeddingModel: status.EmbeddingModel, EmbeddingAPIKeyMask: status.EmbeddingAPIKeyMask, EmbeddingThreshold: status.EmbeddingThreshold, SemanticEnabled: status.SemanticEnabled, EmbeddingHealth: platformmodel.GetEmbeddingHealth(status.WorkspaceID)}
}

func citationResponses(citations []conversation.Citation) []citationResponse {
	responses := make([]citationResponse, 0, len(citations))
	for _, citation := range citations {
		responses = append(responses, citationResponse{
			CitationID:  citation.ID,
			EvidenceID:  citation.EvidenceID,
			SourceID:    citation.SourceID,
			ChunkID:     citation.ChunkID,
			SourceTitle: citation.SourceTitle,
			Quote:       citation.Quote,
		})
	}
	return responses
}

func decodeJSON(
	response http.ResponseWriter,
	request *http.Request,
	maximumBytes int64,
	destination any,
) error {
	request.Body = http.MaxBytesReader(response, request.Body, maximumBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func (h apiHandlers) writeError(
	response http.ResponseWriter,
	status int,
	code string,
	message string,
	requestID string,
) {
	h.writeJSON(response, status, errorEnvelope{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

func (h apiHandlers) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		h.logger.Error("write JSON response", "error", err)
	}
}
