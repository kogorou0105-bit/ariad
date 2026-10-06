package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	dbgen "ariad/db/generated"
	"ariad/internal/knowledge"
)

// KnowledgeRepository persists immutable knowledge sources and chunks.
type KnowledgeRepository struct {
	database *sql.DB
	queries  *dbgen.Queries
}

var _ knowledge.Repository = (*KnowledgeRepository)(nil)

// NewKnowledgeRepository creates a PostgreSQL knowledge adapter.
func NewKnowledgeRepository(database *sql.DB) *KnowledgeRepository {
	return &KnowledgeRepository{database: database, queries: dbgen.New(database)}
}

// FindSubmission finds an idempotent knowledge submission in one workspace.
func (r *KnowledgeRepository) FindSubmission(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (knowledge.SubmissionRecord, bool, error) {
	return findKnowledgeSubmission(ctx, r.queries, workspaceID, idempotencyKey)
}

// SaveSource atomically stores one source and all of its chunks.
func (r *KnowledgeRepository) SaveSource(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	source knowledge.Source,
	chunks []knowledge.Chunk,
) (knowledge.SubmitTextResult, error) {
	if err := validateKnowledgeWrite(workspaceID, idempotencyKey, payloadFingerprint, source, chunks); err != nil {
		return knowledge.SubmitTextResult{}, err
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return knowledge.SubmitTextResult{}, fmt.Errorf("begin knowledge transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := r.queries.WithTx(tx)
	if err := queries.LockKnowledgeSubmission(ctx, dbgen.LockKnowledgeSubmissionParams{
		WorkspaceID:    sql.NullString{String: workspaceID, Valid: true},
		IdempotencyKey: sql.NullString{String: idempotencyKey, Valid: true},
	}); err != nil {
		return knowledge.SubmitTextResult{}, fmt.Errorf("lock knowledge submission: %w", err)
	}
	previous, found, err := findKnowledgeSubmission(ctx, queries, workspaceID, idempotencyKey)
	if err != nil {
		return knowledge.SubmitTextResult{}, err
	}
	if found {
		if previous.PayloadFingerprint != payloadFingerprint || previous.SourceURL != source.SourceURL {
			return knowledge.SubmitTextResult{}, knowledge.ErrIdempotencyConflict
		}
		return previous.Result, nil
	}
	if _, err := queries.InsertKnowledgeSource(ctx, dbgen.InsertKnowledgeSourceParams{
		WorkspaceID:        workspaceID,
		SourceID:           source.ID,
		Title:              source.Title,
		SourceUrl:          nullableString(source.SourceURL),
		IdempotencyKey:     idempotencyKey,
		PayloadFingerprint: payloadFingerprint,
		CreatedAt:          source.CreatedAt,
	}); err != nil {
		return knowledge.SubmitTextResult{}, fmt.Errorf("insert knowledge source: %w", err)
	}
	for _, chunk := range chunks {
		ordinal, ordinalErr := checkedInt32(chunk.Ordinal)
		if ordinalErr != nil {
			return knowledge.SubmitTextResult{}, ordinalErr
		}
		if err := queries.InsertKnowledgeChunk(ctx, dbgen.InsertKnowledgeChunkParams{
			WorkspaceID: workspaceID,
			ChunkID:     chunk.ID,
			SourceID:    chunk.SourceID,
			Ordinal:     ordinal,
			Text:        chunk.Text,
		}); err != nil {
			return knowledge.SubmitTextResult{}, fmt.Errorf("insert knowledge chunk: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return knowledge.SubmitTextResult{}, fmt.Errorf("commit knowledge transaction: %w", err)
	}
	return knowledge.SubmitTextResult{
		WorkspaceID: workspaceID,
		SourceID:    source.ID,
		ChunkCount:  len(chunks),
	}, nil
}

// ListChunks returns the immutable chunks visible to one workspace.
func (r *KnowledgeRepository) ListChunks(
	ctx context.Context,
	workspaceID string,
) ([]knowledge.Chunk, error) {
	rows, err := r.queries.ListKnowledgeChunks(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge chunks: %w", err)
	}
	chunks := make([]knowledge.Chunk, 0, len(rows))
	for _, row := range rows {
		chunks = append(chunks, knowledge.Chunk{
			ID:          row.ChunkID,
			WorkspaceID: row.WorkspaceID,
			SourceID:    row.SourceID,
			SourceTitle: row.SourceTitle,
			Ordinal:     int(row.Ordinal),
			Text:        row.Text,
		})
	}
	return chunks, nil
}

func findKnowledgeSubmission(
	ctx context.Context,
	queries *dbgen.Queries,
	workspaceID string,
	idempotencyKey string,
) (knowledge.SubmissionRecord, bool, error) {
	row, err := queries.GetKnowledgeSubmission(ctx, dbgen.GetKnowledgeSubmissionParams{
		WorkspaceID:    workspaceID,
		IdempotencyKey: idempotencyKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.SubmissionRecord{}, false, nil
	}
	if err != nil {
		return knowledge.SubmissionRecord{}, false, fmt.Errorf("get knowledge submission: %w", err)
	}
	if row.ChunkCount < 0 || row.ChunkCount > math.MaxInt {
		return knowledge.SubmissionRecord{}, false, errors.New("knowledge chunk count exceeds platform limits")
	}
	return knowledge.SubmissionRecord{
		Result: knowledge.SubmitTextResult{
			WorkspaceID: workspaceID,
			SourceID:    row.SourceID,
			ChunkCount:  int(row.ChunkCount),
		},
		SourceTitle:        row.Title,
		SourceURL:          row.SourceUrl.String,
		PayloadFingerprint: row.PayloadFingerprint,
	}, true, nil
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func validateKnowledgeWrite(
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	source knowledge.Source,
	chunks []knowledge.Chunk,
) error {
	if workspaceID == "" || idempotencyKey == "" || payloadFingerprint == "" {
		return errors.New("knowledge persistence context is incomplete")
	}
	if source.WorkspaceID != workspaceID {
		return errors.New("source workspace does not match persistence workspace")
	}
	for _, chunk := range chunks {
		if chunk.WorkspaceID != workspaceID || chunk.SourceID != source.ID {
			return errors.New("chunk scope does not match source scope")
		}
	}
	return nil
}

func checkedInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("value %d exceeds PostgreSQL integer limits", value)
	}
	return int32(value), nil
}
