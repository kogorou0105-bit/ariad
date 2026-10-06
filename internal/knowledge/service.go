package knowledge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maximumChunkRunes = 800

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
	CreatedAt   time.Time
}

// Chunk is a normalized, immutable section of a source.
type Chunk struct {
	ID          string
	WorkspaceID string
	SourceID    string
	SourceTitle string
	Ordinal     int
	Text        string
}

// SubmitTextCommand contains the tenant and retry context for a text submission.
type SubmitTextCommand struct {
	WorkspaceID    string
	RequestID      string
	IdempotencyKey string
	Title          string
	Text           string
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
}

// ChunkReader exposes only the knowledge operation required by retrieval.
type ChunkReader interface {
	ListChunks(ctx context.Context, workspaceID string) ([]Chunk, error)
}

// Service accepts text and exposes immutable chunks.
type Service struct {
	repository Repository
	clock      func() time.Time
}

// NewService creates a knowledge service.
func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: time.Now}
}

// SubmitText creates an immutable source and its chunks, or replays the result
// for a duplicate idempotency key in the same workspace.
func (s *Service) SubmitText(
	ctx context.Context,
	command SubmitTextCommand,
) (SubmitTextResult, error) {
	text := strings.TrimSpace(command.Text)
	if text == "" {
		return SubmitTextResult{}, ErrInvalidText
	}
	if command.WorkspaceID == "" || command.IdempotencyKey == "" {
		return SubmitTextResult{}, errors.New("workspace and idempotency key are required")
	}
	title := strings.TrimSpace(command.Title)
	if title == "" {
		title = "Pasted text"
	}
	payloadFingerprint := fingerprint(title, text)

	previous, found, err := s.repository.FindSubmission(
		ctx,
		command.WorkspaceID,
		command.IdempotencyKey,
	)
	if err != nil {
		return SubmitTextResult{}, fmt.Errorf("find knowledge submission: %w", err)
	}
	if found {
		if previous.PayloadFingerprint != payloadFingerprint {
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
		CreatedAt:   s.clock().UTC(),
	}

	parts := splitText(text, maximumChunkRunes)
	chunks := make([]Chunk, 0, len(parts))
	for ordinal, part := range parts {
		chunkID, chunkErr := randomID("chk")
		if chunkErr != nil {
			return SubmitTextResult{}, chunkErr
		}
		chunks = append(chunks, Chunk{
			ID:          chunkID,
			WorkspaceID: command.WorkspaceID,
			SourceID:    source.ID,
			SourceTitle: source.Title,
			Ordinal:     ordinal,
			Text:        part,
		})
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

// ListChunks returns a copy of every chunk visible to the workspace.
func (s *Service) ListChunks(ctx context.Context, workspaceID string) ([]Chunk, error) {
	return s.repository.ListChunks(ctx, workspaceID)
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
