package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"ariad/internal/ingestion"
	"ariad/internal/knowledge"
	platformfetch "ariad/internal/platform/fetch"

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
