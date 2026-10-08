package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"ariad/internal/conversation"
	"ariad/internal/review"
	"ariad/internal/runtime"
	"ariad/internal/visitor"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

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
