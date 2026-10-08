package main

import (
	"errors"
	"net/http"
	"time"

	"ariad/internal/visitor"

	"github.com/go-chi/chi/v5/middleware"
)

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
