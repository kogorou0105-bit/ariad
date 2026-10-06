package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ariad/internal/agent"
	"ariad/internal/conversation"
	"ariad/internal/ingestion"
	"ariad/internal/knowledge"
	"ariad/internal/platform/config"
	"ariad/internal/platform/database"
	platformfetch "ariad/internal/platform/fetch"
	"ariad/internal/platform/health"
	platformmodel "ariad/internal/platform/model"
	"ariad/internal/platform/observability"
	"ariad/internal/retrieval"
	"ariad/internal/runtime"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	developmentWorkspaceID = "ws_dev"
	developmentAgentID     = "agent_dev"
	maximumKnowledgeBody   = 1 << 20
	maximumIngestionBody   = 16 << 10
	maximumQuestionBody    = 64 << 10
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
	return newRouterWithRepositories(
		logger,
		modelAdapter,
		knowledge.NewMemoryRepository(),
		conversation.NewMemoryRepository(),
		platformfetch.NewHTTPFetcher(),
		0,
	)
}

func newConfiguredRouter(
	ctx context.Context,
	logger *slog.Logger,
	modelAdapter runtime.Model,
	databaseURL string,
	historyTurnLimit int,
) (http.Handler, func() error, error) {
	if databaseURL == "" {
		return newRouterWithRepositories(
			logger,
			modelAdapter,
			knowledge.NewMemoryRepository(),
			conversation.NewMemoryRepository(),
			platformfetch.NewHTTPFetcher(),
			historyTurnLimit,
		), func() error { return nil }, nil
	}
	postgres, err := database.Open(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect persistence: %w", err)
	}
	if err := database.VerifySchemaReady(ctx, postgres); err != nil {
		_ = postgres.Close()
		return nil, nil, err
	}
	handler := newRouterWithRepositories(
		logger,
		modelAdapter,
		database.NewKnowledgeRepository(postgres),
		database.NewConversationRepository(postgres),
		platformfetch.NewHTTPFetcher(),
		historyTurnLimit,
	)
	return handler, postgres.Close, nil
}

func newRouterWithRepositories(
	logger *slog.Logger,
	modelAdapter runtime.Model,
	knowledgeRepository knowledge.Repository,
	conversationRepository conversation.Repository,
	pageFetcher ingestion.Fetcher,
	historyTurnLimit int,
) http.Handler {
	knowledgeService := knowledge.NewService(knowledgeRepository)
	ingestionService := ingestion.NewService(pageFetcher, knowledgeService)
	retrievalService := retrieval.NewService(knowledgeService)
	agentReader := agent.NewStaticReader([]agent.PublishedAgent{{
		WorkspaceID: developmentWorkspaceID,
		AgentID:     developmentAgentID,
		VersionID:   "agent_version_dev",
		Name:        "Ariad Support",
		Instructions: "Answer only from the supplied evidence. " +
			"Never invent facts and cite every factual answer.",
	}})
	runtimeService := runtime.NewService(agentReader, retrievalService, modelAdapter)
	conversationService := conversation.NewService(
		conversationRepository,
		runtimeService,
		conversation.WithHistoryTurnLimit(historyTurnLimit),
	)
	// TODO(auth): replace this development-only scope with workspace and agent
	// identifiers derived from an authenticated token/session. Request-body IDs
	// are compatibility fields to validate, never the authority used downstream.
	scope := requestScope{workspaceID: developmentWorkspaceID, agentID: developmentAgentID}
	return newRouterWithServices(logger, scope, knowledgeService, ingestionService, conversationService)
}

func newRouterWithServices(
	logger *slog.Logger,
	scope requestScope,
	knowledgeService *knowledge.Service,
	ingestionService ingestion.URLSubmitter,
	conversationService conversation.QuestionService,
) http.Handler {
	handlers := apiHandlers{
		logger:       logger,
		scope:        scope,
		knowledge:    knowledgeService,
		ingestion:    ingestionService,
		conversation: conversationService,
	}
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
		bounded.Post("/api/v1/knowledge/text", handlers.submitKnowledge)
		bounded.Post("/api/v1/ingestion/url", handlers.submitURL)
		bounded.Post("/api/v1/questions", handlers.submitQuestion)
		bounded.Get("/api/v1/conversations/{conversation_id}", handlers.listConversationTurns)
	})

	return router
}

type apiHandlers struct {
	logger       *slog.Logger
	scope        requestScope
	knowledge    *knowledge.Service
	ingestion    ingestion.URLSubmitter
	conversation conversation.QuestionService
}

type requestScope struct {
	workspaceID string
	agentID     string
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
	VisitorID      string `json:"visitor_id"`
	Channel        string `json:"channel"`
	Locale         string `json:"locale"`
	RequestID      string `json:"request_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Question       string `json:"question"`
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
	Turns          []conversationTurnResponse `json:"turns"`
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
	if input.VisitorID == "" || input.RequestID == "" || input.IdempotencyKey == "" {
		h.writeError(
			response,
			http.StatusBadRequest,
			"invalid_request",
			"visitor_id, request_id and idempotency_key are required",
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
		VisitorID:      input.VisitorID,
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
	if visitorID == "" {
		h.writeError(response, http.StatusBadRequest, "invalid_request", "visitor_id is required", requestID)
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
	h.writeJSON(response, http.StatusOK, listConversationTurnsResponse{
		WorkspaceID:    h.scope.workspaceID,
		ConversationID: conversationID,
		Turns:          turnResponses,
	})
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
