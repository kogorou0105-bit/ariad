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
	"ariad/internal/knowledge"
	"ariad/internal/platform/config"
	"ariad/internal/platform/health"
	platformmodel "ariad/internal/platform/model"
	"ariad/internal/platform/observability"
	"ariad/internal/retrieval"
	"ariad/internal/runtime"
	"ariad/internal/usage"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	developmentWorkspaceID = "ws_dev"
	developmentAgentID     = "agent_dev"
	maximumKnowledgeBody   = 1 << 20
	maximumQuestionBody    = 64 << 10
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
		Handler:           newRouter(logger),
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

func newRouter(logger *slog.Logger) http.Handler {
	knowledgeService := knowledge.NewService(knowledge.NewMemoryRepository())
	retrievalService := retrieval.NewService(knowledgeService)
	agentReader := agent.NewStaticReader([]agent.PublishedAgent{{
		WorkspaceID: developmentWorkspaceID,
		AgentID:     developmentAgentID,
		VersionID:   "agent_version_dev",
		Name:        "Ariad Support",
		Instructions: "Answer only from the supplied evidence. " +
			"Never invent facts and cite every factual answer.",
	}})
	runtimeService := runtime.NewService(agentReader, retrievalService, platformmodel.NewStub())
	conversationService := conversation.NewService(
		conversation.NewMemoryRepository(),
		runtimeService,
		usage.NewNoopRecorder(),
	)
	// TODO(auth): replace this development-only scope with workspace and agent
	// identifiers derived from an authenticated token/session. Request-body IDs
	// are compatibility fields to validate, never the authority used downstream.
	scope := requestScope{workspaceID: developmentWorkspaceID, agentID: developmentAgentID}
	return newRouterWithServices(logger, scope, knowledgeService, conversationService)
}

func newRouterWithServices(
	logger *slog.Logger,
	scope requestScope,
	knowledgeService *knowledge.Service,
	conversationService conversation.QuestionSubmitter,
) http.Handler {
	handlers := apiHandlers{
		logger:       logger,
		scope:        scope,
		knowledge:    knowledgeService,
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
		bounded.Post("/api/v1/questions", handlers.submitQuestion)
	})

	return router
}

type apiHandlers struct {
	logger       *slog.Logger
	scope        requestScope
	knowledge    *knowledge.Service
	conversation conversation.QuestionSubmitter
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

	citations := make([]citationResponse, 0, len(turn.Answer.Citations))
	for _, citation := range turn.Answer.Citations {
		citations = append(citations, citationResponse{
			CitationID:  citation.ID,
			EvidenceID:  citation.EvidenceID,
			SourceID:    citation.SourceID,
			ChunkID:     citation.ChunkID,
			SourceTitle: citation.SourceTitle,
			Quote:       citation.Quote,
		})
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
		Citations:           citations,
	})
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
