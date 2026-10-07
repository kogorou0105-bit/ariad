package knowledge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	maximumChunkRunes      = 800
	embeddingBackfillLease = 5 * time.Minute
)

// ErrInvalidText indicates that a knowledge submission contains no usable text.
var (
	ErrInvalidText         = errors.New("knowledge text is required")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different knowledge payload")
)

// Source is an immutable submitted text source.
type Source struct {
	ID          string
	WorkspaceID string
	Title       string
	SourceURL   string
	Type        string
	Status      string
	FileName    string
	MediaType   string
	FileSize    int64
	FileContent []byte
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SourceSummary struct {
	Source
	ChunkCount int
}

// Chunk is a normalized, immutable section of a source.
type Chunk struct {
	ID          string
	WorkspaceID string
	SourceID    string
	SourceTitle string
	Ordinal     int
	Text        string
	Embedding   []float64
}

type Embedder interface {
	Embed(context.Context, string, string) ([]float64, error)
}

// SubmitTextCommand contains the tenant and retry context for a text submission.
type SubmitTextCommand struct {
	WorkspaceID     string
	RequestID       string
	IdempotencyKey  string
	Title           string
	SourceURL       string
	Text            string
	SourceType      string
	FileName        string
	MediaType       string
	FileContent     []byte
	ProcessingError string
}

// SubmitTextResult identifies the immutable source produced by a submission.
type SubmitTextResult struct {
	WorkspaceID string
	SourceID    string
	ChunkCount  int
}

// SubmissionRecord stores the result and payload identity for strict replay validation.
type SubmissionRecord struct {
	Result             SubmitTextResult
	SourceTitle        string
	SourceURL          string
	PayloadFingerprint string
}

// Repository is shaped around workspace-scoped operations that can later be
// implemented transactionally in PostgreSQL.
type Repository interface {
	FindSubmission(
		ctx context.Context,
		workspaceID string,
		idempotencyKey string,
	) (SubmissionRecord, bool, error)
	SaveSource(
		ctx context.Context,
		workspaceID string,
		idempotencyKey string,
		payloadFingerprint string,
		source Source,
		chunks []Chunk,
	) (SubmitTextResult, error)
	ListChunks(ctx context.Context, workspaceID string) ([]Chunk, error)
	SaveEmbedding(ctx context.Context, workspaceID, chunkID string, embedding []float64) error
	GetEmbeddingBackfill(ctx context.Context, workspaceID string) (EmbeddingBackfillStatus, bool, error)
	SaveEmbeddingBackfill(ctx context.Context, workspaceID string, status EmbeddingBackfillStatus) error
	ListSources(ctx context.Context, workspaceID string) ([]SourceSummary, error)
	GetSource(ctx context.Context, workspaceID, sourceID string) (SourceSummary, bool, error)
	DeleteSource(ctx context.Context, workspaceID, sourceID string) (bool, error)
	ReplaceSourceChunks(ctx context.Context, source Source, chunks []Chunk) error
}

type EmbeddingFailure struct {
	ChunkID string
	Reason  string
}

type EmbeddingBackfillStatus struct {
	Status    string
	Total     int
	Completed int
	Failed    int
	Failures  []EmbeddingFailure
	Error     string
	UpdatedAt time.Time
}

type BackfillResult struct {
	Total     int
	Completed int
	Failed    int
	Failures  []EmbeddingFailure
}

func (s *Service) BackfillEmbeddings(ctx context.Context, workspaceID string) (BackfillResult, error) {
	status := EmbeddingBackfillStatus{Status: "running", UpdatedAt: s.clock().UTC()}
	if err := s.repository.SaveEmbeddingBackfill(ctx, workspaceID, status); err != nil {
		return BackfillResult{}, fmt.Errorf("save embedding backfill status: %w", err)
	}
	chunks, err := s.repository.ListChunks(ctx, workspaceID)
	if err != nil {
		s.failEmbeddingBackfill(ctx, workspaceID, status, err)
		return BackfillResult{}, err
	}
	result := BackfillResult{Total: len(chunks)}
	status.Total = result.Total
	if s.embedder == nil {
		err = errors.New("semantic retrieval is not configured")
		s.failEmbeddingBackfill(ctx, workspaceID, status, err)
		return result, err
	}
	for _, chunk := range chunks {
		if len(chunk.Embedding) > 0 {
			result.Completed++
		} else {
			vector, embedErr := s.embedder.Embed(ctx, workspaceID, chunk.Text)
			if embedErr == nil && len(vector) == 0 {
				embedErr = errors.New("embedding provider returned an empty vector")
			}
			if embedErr == nil {
				embedErr = s.repository.SaveEmbedding(ctx, workspaceID, chunk.ID, vector)
			}
			if embedErr != nil {
				result.Failed++
				result.Failures = append(result.Failures, EmbeddingFailure{ChunkID: chunk.ID, Reason: embedErr.Error()})
				s.reportEmbeddingError(workspaceID, chunk.ID, embedErr)
			} else {
				result.Completed++
			}
		}
		status.Completed, status.Failed, status.Failures, status.UpdatedAt = result.Completed, result.Failed, append([]EmbeddingFailure(nil), result.Failures...), s.clock().UTC()
		if err := s.repository.SaveEmbeddingBackfill(ctx, workspaceID, status); err != nil {
			return result, fmt.Errorf("update embedding backfill status: %w", err)
		}
	}
	status.Status, status.UpdatedAt = "completed", s.clock().UTC()
	if err := s.repository.SaveEmbeddingBackfill(ctx, workspaceID, status); err != nil {
		return result, fmt.Errorf("complete embedding backfill status: %w", err)
	}
	return result, nil
}

func (s *Service) StartEmbeddingBackfill(ctx context.Context, workspaceID string) (EmbeddingBackfillStatus, bool, error) {
	current, found, err := s.repository.GetEmbeddingBackfill(ctx, workspaceID)
	if err != nil {
		return EmbeddingBackfillStatus{}, false, err
	}
	if found && current.Status == "running" && s.clock().UTC().Sub(current.UpdatedAt) < embeddingBackfillLease {
		return current, false, nil
	}
	status := EmbeddingBackfillStatus{Status: "running", UpdatedAt: s.clock().UTC()}
	if err := s.repository.SaveEmbeddingBackfill(ctx, workspaceID, status); err != nil {
		return EmbeddingBackfillStatus{}, false, err
	}
	return status, true, nil
}

func (s *Service) GetEmbeddingBackfill(ctx context.Context, workspaceID string) (EmbeddingBackfillStatus, bool, error) {
	return s.repository.GetEmbeddingBackfill(ctx, workspaceID)
}

func (s *Service) failEmbeddingBackfill(ctx context.Context, workspaceID string, status EmbeddingBackfillStatus, failure error) {
	status.Status, status.Error, status.UpdatedAt = "failed", failure.Error(), s.clock().UTC()
	_ = s.repository.SaveEmbeddingBackfill(ctx, workspaceID, status)
}

// ChunkReader exposes only the knowledge operation required by retrieval.
type ChunkReader interface {
	ListChunks(ctx context.Context, workspaceID string) ([]Chunk, error)
}

// Submitter exposes only the knowledge operation required by ingestion.
type Submitter interface {
	FindSubmission(
		ctx context.Context,
		workspaceID string,
		idempotencyKey string,
	) (SubmissionRecord, bool, error)
	SubmitText(ctx context.Context, command SubmitTextCommand) (SubmitTextResult, error)
}

// Service accepts text and exposes immutable chunks.
type Service struct {
	repository       Repository
	clock            func() time.Time
	embedder         Embedder
	onEmbeddingError func(workspaceID, chunkID string, err error)
}

type Option func(*Service)

func WithEmbedder(embedder Embedder) Option {
	return func(service *Service) { service.embedder = embedder }
}

func WithEmbeddingErrorHandler(handler func(workspaceID, chunkID string, err error)) Option {
	return func(service *Service) { service.onEmbeddingError = handler }
}

// NewService creates a knowledge service.
func NewService(repository Repository, options ...Option) *Service {
	service := &Service{repository: repository, clock: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

// FindSubmission exposes an idempotency lookup without exposing persistence.
func (s *Service) FindSubmission(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (SubmissionRecord, bool, error) {
	return s.repository.FindSubmission(ctx, workspaceID, idempotencyKey)
}

// SubmitText creates an immutable source and its chunks, or replays the result
// for a duplicate idempotency key in the same workspace.
func (s *Service) SubmitText(
	ctx context.Context,
	command SubmitTextCommand,
) (SubmitTextResult, error) {
	text := strings.TrimSpace(command.Text)
	if text == "" && command.ProcessingError == "" {
		return SubmitTextResult{}, ErrInvalidText
	}
	if command.WorkspaceID == "" || command.IdempotencyKey == "" {
		return SubmitTextResult{}, errors.New("workspace and idempotency key are required")
	}
	title := strings.TrimSpace(command.Title)
	sourceURL := strings.TrimSpace(command.SourceURL)
	if title == "" {
		title = "Pasted text"
	}
	payloadFingerprint := fingerprint(title, text)
	if command.SourceType == "file" {
		payloadFingerprint = fingerprint("file", strings.ToLower(filepath.Ext(command.FileName)), string(command.FileContent))
	}

	previous, found, err := s.repository.FindSubmission(
		ctx,
		command.WorkspaceID,
		command.IdempotencyKey,
	)
	if err != nil {
		return SubmitTextResult{}, fmt.Errorf("find knowledge submission: %w", err)
	}
	if found {
		if previous.PayloadFingerprint != payloadFingerprint || previous.SourceURL != sourceURL {
			return SubmitTextResult{}, ErrIdempotencyConflict
		}
		return previous.Result, nil
	}

	sourceID, err := randomID("src")
	if err != nil {
		return SubmitTextResult{}, err
	}
	source := Source{
		ID:          sourceID,
		WorkspaceID: command.WorkspaceID,
		Title:       title,
		SourceURL:   sourceURL,
		Type:        command.SourceType,
		Status:      "ready",
		FileName:    command.FileName,
		MediaType:   command.MediaType,
		FileSize:    int64(len(command.FileContent)),
		FileContent: append([]byte(nil), command.FileContent...),
		Error:       command.ProcessingError,
		CreatedAt:   s.clock().UTC(),
		UpdatedAt:   s.clock().UTC(),
	}
	if source.Type == "" {
		source.Type = "text"
	}
	if source.Error != "" {
		source.Status = "failed"
	}

	parts := splitText(text, maximumChunkRunes)
	chunks := make([]Chunk, 0, len(parts))
	for ordinal, part := range parts {
		chunkID, chunkErr := randomID("chk")
		if chunkErr != nil {
			return SubmitTextResult{}, chunkErr
		}
		chunk := Chunk{
			ID:          chunkID,
			WorkspaceID: command.WorkspaceID,
			SourceID:    source.ID,
			SourceTitle: source.Title,
			Ordinal:     ordinal,
			Text:        part,
		}
		if s.embedder != nil {
			if vector, embeddingErr := s.embedder.Embed(ctx, command.WorkspaceID, part); embeddingErr == nil {
				chunk.Embedding = vector
			} else {
				s.reportEmbeddingError(command.WorkspaceID, chunk.ID, embeddingErr)
			}
		}
		chunks = append(chunks, chunk)
	}

	result, err := s.repository.SaveSource(
		ctx,
		command.WorkspaceID,
		command.IdempotencyKey,
		payloadFingerprint,
		source,
		chunks,
	)
	if err != nil {
		return SubmitTextResult{}, fmt.Errorf("save knowledge source: %w", err)
	}
	return result, nil
}

func (s *Service) reportEmbeddingError(workspaceID, chunkID string, err error) {
	if err != nil && s.onEmbeddingError != nil {
		s.onEmbeddingError(workspaceID, chunkID, err)
	}
}

// ListChunks returns a copy of every chunk visible to the workspace.
func (s *Service) ListChunks(ctx context.Context, workspaceID string) ([]Chunk, error) {
	return s.repository.ListChunks(ctx, workspaceID)
}

func (s *Service) ListSources(ctx context.Context, workspaceID string) ([]SourceSummary, error) {
	return s.repository.ListSources(ctx, workspaceID)
}

func (s *Service) GetSource(ctx context.Context, workspaceID, sourceID string) (SourceSummary, bool, error) {
	return s.repository.GetSource(ctx, workspaceID, sourceID)
}

func (s *Service) DeleteSource(ctx context.Context, workspaceID, sourceID string) (bool, error) {
	return s.repository.DeleteSource(ctx, workspaceID, sourceID)
}

func (s *Service) ReprocessSource(ctx context.Context, source Source, text string) error {
	parts := splitText(strings.TrimSpace(text), maximumChunkRunes)
	if len(parts) == 0 {
		return ErrInvalidText
	}
	chunks := make([]Chunk, 0, len(parts))
	for ordinal, part := range parts {
		chunkID, err := randomID("chk")
		if err != nil {
			return err
		}
		chunk := Chunk{ID: chunkID, WorkspaceID: source.WorkspaceID, SourceID: source.ID, SourceTitle: source.Title, Ordinal: ordinal, Text: part}
		if s.embedder != nil {
			if vector, err := s.embedder.Embed(ctx, source.WorkspaceID, part); err == nil {
				chunk.Embedding = vector
			} else {
				s.reportEmbeddingError(source.WorkspaceID, chunk.ID, err)
			}
		}
		chunks = append(chunks, chunk)
	}
	source.Status, source.Error, source.UpdatedAt = "ready", "", s.clock().UTC()
	return s.repository.ReplaceSourceChunks(ctx, source, chunks)
}

func randomID(prefix string) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate %s identifier: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

func fingerprint(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(digest, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
