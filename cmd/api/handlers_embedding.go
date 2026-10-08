package main

import (
	"context"
	"net/http"
	"time"

	"ariad/internal/knowledge"

	"github.com/go-chi/chi/v5/middleware"
)

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
