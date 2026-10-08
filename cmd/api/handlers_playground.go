package main

import (
	"errors"
	"net/http"

	"ariad/internal/playground"

	"github.com/go-chi/chi/v5/middleware"
)

type playgroundAskRequest struct {
	WorkspaceID string  `json:"workspace_id"`
	Question    string  `json:"question"`
	TopK        int     `json:"top_k"`
	Threshold   float64 `json:"threshold"`
	Locale      string  `json:"locale"`
}

type playgroundHitResponse struct {
	EvidenceID  string  `json:"evidence_id"`
	SourceID    string  `json:"source_id"`
	ChunkID     string  `json:"chunk_id"`
	SourceTitle string  `json:"source_title"`
	Text        string  `json:"text"`
	Score       float64 `json:"score"`
}

type playgroundAskResponse struct {
	Outcome             string                  `json:"outcome"`
	Answer              string                  `json:"answer"`
	Hits                []playgroundHitResponse `json:"hits"`
	HitCount            int                     `json:"hit_count"`
	DurationMS          int64                   `json:"duration_ms"`
	RetrievalDurationMS int64                   `json:"retrieval_duration_ms"`
	ModelDurationMS     int64                   `json:"model_duration_ms"`
}

func (h apiHandlers) askPlayground(response http.ResponseWriter, request *http.Request) {
	var input playgroundAskRequest
	if err := decodeJSON(response, request, maximumQuestionBody, &input); err != nil {
		h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		return
	}
	if input.WorkspaceID != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", middleware.GetReqID(request.Context()))
		return
	}
	result, err := h.playground.Ask(request.Context(), playground.Command{WorkspaceID: h.scope.workspaceID, AgentID: h.scope.agentID, Question: input.Question, TopK: input.TopK, Threshold: input.Threshold, Locale: input.Locale})
	if err != nil {
		switch {
		case errors.Is(err, playground.ErrInvalidQuestion), errors.Is(err, playground.ErrInvalidTopK), errors.Is(err, playground.ErrInvalidThreshold):
			h.writeError(response, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(request.Context()))
		case errors.Is(err, playground.ErrModelNotConfigured):
			h.writeError(response, http.StatusPreconditionFailed, "model_not_configured", err.Error(), middleware.GetReqID(request.Context()))
		case errors.Is(err, playground.ErrNoKnowledge):
			h.writeError(response, http.StatusUnprocessableEntity, "no_knowledge", err.Error(), middleware.GetReqID(request.Context()))
		default:
			h.writeError(response, http.StatusInternalServerError, "playground_failed", "could not run playground question", middleware.GetReqID(request.Context()))
		}
		return
	}
	hits := make([]playgroundHitResponse, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hits = append(hits, playgroundHitResponse{EvidenceID: hit.EvidenceID, SourceID: hit.SourceID, ChunkID: hit.ChunkID, SourceTitle: hit.SourceTitle, Text: hit.Text, Score: hit.Score})
	}
	h.writeJSON(response, http.StatusOK, playgroundAskResponse{Outcome: result.Outcome, Answer: result.Answer, Hits: hits, HitCount: result.HitCount, DurationMS: result.Duration.Milliseconds(), RetrievalDurationMS: result.RetrievalDuration.Milliseconds(), ModelDurationMS: result.ModelDuration.Milliseconds()})
}
