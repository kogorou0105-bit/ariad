package main

import (
	"errors"
	"net/http"

	"ariad/internal/modelconfig"
	platformmodel "ariad/internal/platform/model"

	"github.com/go-chi/chi/v5/middleware"
)

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
