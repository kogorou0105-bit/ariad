package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

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
	if source.Status == "" {
		source.Status = "ready"
	}
	if source.Type == "" {
		source.Type = "text"
	}
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
		SourceType:         source.Type,
		Status:             source.Status,
		FileName:           nullableString(source.FileName),
		MediaType:          nullableString(source.MediaType),
		FileSize:           sql.NullInt64{Int64: source.FileSize, Valid: source.FileName != ""},
		FileContent:        source.FileContent,
		ErrorMessage:       nullableString(source.Error),
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
		if len(chunk.Embedding) > 0 {
			encoded, encodeErr := json.Marshal(chunk.Embedding)
			if encodeErr != nil {
				return knowledge.SubmitTextResult{}, encodeErr
			}
			if err := queries.UpsertKnowledgeChunkEmbedding(ctx, dbgen.UpsertKnowledgeChunkEmbeddingParams{WorkspaceID: workspaceID, ChunkID: chunk.ID, Embedding: string(encoded), UpdatedAt: time.Now().UTC()}); err != nil {
				return knowledge.SubmitTextResult{}, fmt.Errorf("insert knowledge embedding: %w", err)
			}
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

func sourceFromRow(workspaceID, sourceID, title string, sourceURL sql.NullString, sourceType, status string, fileName, mediaType sql.NullString, fileSize sql.NullInt64, fileContent []byte, errorMessage sql.NullString, createdAt, updatedAt time.Time) knowledge.Source {
	return knowledge.Source{ID: sourceID, WorkspaceID: workspaceID, Title: title, SourceURL: sourceURL.String, Type: sourceType, Status: status, FileName: fileName.String, MediaType: mediaType.String, FileSize: fileSize.Int64, FileContent: fileContent, Error: errorMessage.String, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func (r *KnowledgeRepository) ListSources(ctx context.Context, workspaceID string) ([]knowledge.SourceSummary, error) {
	rows, err := r.queries.ListKnowledgeSources(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]knowledge.SourceSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, knowledge.SourceSummary{Source: sourceFromRow(row.WorkspaceID, row.SourceID, row.Title, row.SourceUrl, row.SourceType, row.Status, row.FileName, row.MediaType, row.FileSize, nil, row.ErrorMessage, row.CreatedAt, row.UpdatedAt), ChunkCount: int(row.ChunkCount)})
	}
	return result, nil
}

func (r *KnowledgeRepository) GetSource(ctx context.Context, workspaceID, sourceID string) (knowledge.SourceSummary, bool, error) {
	row, err := r.queries.GetKnowledgeSource(ctx, dbgen.GetKnowledgeSourceParams{WorkspaceID: workspaceID, SourceID: sourceID})
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.SourceSummary{}, false, nil
	}
	if err != nil {
		return knowledge.SourceSummary{}, false, err
	}
	return knowledge.SourceSummary{Source: sourceFromRow(row.WorkspaceID, row.SourceID, row.Title, row.SourceUrl, row.SourceType, row.Status, row.FileName, row.MediaType, row.FileSize, row.FileContent, row.ErrorMessage, row.CreatedAt, row.UpdatedAt), ChunkCount: int(row.ChunkCount)}, true, nil
}

func (r *KnowledgeRepository) DeleteSource(ctx context.Context, workspaceID, sourceID string) (bool, error) {
	count, err := r.queries.DeleteKnowledgeSource(ctx, dbgen.DeleteKnowledgeSourceParams{WorkspaceID: workspaceID, SourceID: sourceID})
	return count > 0, err
}

func (r *KnowledgeRepository) ReplaceSourceChunks(ctx context.Context, source knowledge.Source, chunks []knowledge.Chunk) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	queries := r.queries.WithTx(tx)
	if err := queries.DeleteKnowledgeSourceChunks(ctx, dbgen.DeleteKnowledgeSourceChunksParams{WorkspaceID: source.WorkspaceID, SourceID: source.ID}); err != nil {
		return err
	}
	for _, chunk := range chunks {
		ordinal, err := checkedInt32(chunk.Ordinal)
		if err != nil {
			return err
		}
		if err := queries.InsertKnowledgeChunk(ctx, dbgen.InsertKnowledgeChunkParams{WorkspaceID: source.WorkspaceID, ChunkID: chunk.ID, SourceID: source.ID, Ordinal: ordinal, Text: chunk.Text}); err != nil {
			return err
		}
		if len(chunk.Embedding) > 0 {
			encoded, _ := json.Marshal(chunk.Embedding)
			if err := queries.UpsertKnowledgeChunkEmbedding(ctx, dbgen.UpsertKnowledgeChunkEmbeddingParams{WorkspaceID: source.WorkspaceID, ChunkID: chunk.ID, Embedding: string(encoded), UpdatedAt: source.UpdatedAt}); err != nil {
				return err
			}
		}
	}
	if err := queries.UpdateKnowledgeSourceResult(ctx, dbgen.UpdateKnowledgeSourceResultParams{Status: source.Status, ErrorMessage: nullableString(source.Error), UpdatedAt: source.UpdatedAt, WorkspaceID: source.WorkspaceID, SourceID: source.ID}); err != nil {
		return err
	}
	return tx.Commit()
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
		chunk := knowledge.Chunk{
			ID:          row.ChunkID,
			WorkspaceID: row.WorkspaceID,
			SourceID:    row.SourceID,
			SourceTitle: row.SourceTitle,
			Ordinal:     int(row.Ordinal),
			Text:        row.Text,
		}
		if row.Embedding.Valid {
			if err := json.Unmarshal([]byte(row.Embedding.String), &chunk.Embedding); err != nil {
				return nil, fmt.Errorf("decode knowledge chunk embedding %s: %w", row.ChunkID, err)
			}
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (r *KnowledgeRepository) SaveEmbedding(ctx context.Context, workspaceID, chunkID string, embedding []float64) error {
	encoded, err := json.Marshal(embedding)
	if err != nil {
		return err
	}
	return r.queries.UpsertKnowledgeChunkEmbedding(ctx, dbgen.UpsertKnowledgeChunkEmbeddingParams{WorkspaceID: workspaceID, ChunkID: chunkID, Embedding: string(encoded), UpdatedAt: time.Now().UTC()})
}

func (r *KnowledgeRepository) GetEmbeddingBackfill(ctx context.Context, workspaceID string) (knowledge.EmbeddingBackfillStatus, bool, error) {
	row, err := r.queries.GetKnowledgeEmbeddingBackfill(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.EmbeddingBackfillStatus{}, false, nil
	}
	if err != nil {
		return knowledge.EmbeddingBackfillStatus{}, false, fmt.Errorf("get knowledge embedding backfill: %w", err)
	}
	var failures []knowledge.EmbeddingFailure
	if err := json.Unmarshal([]byte(row.Failures), &failures); err != nil {
		return knowledge.EmbeddingBackfillStatus{}, false, fmt.Errorf("decode knowledge embedding backfill failures: %w", err)
	}
	return knowledge.EmbeddingBackfillStatus{
		Status: row.Status, Total: int(row.Total), Completed: int(row.Completed), Failed: int(row.Failed),
		Failures: failures, Error: row.Error.String, UpdatedAt: row.UpdatedAt,
	}, true, nil
}

func (r *KnowledgeRepository) SaveEmbeddingBackfill(ctx context.Context, workspaceID string, status knowledge.EmbeddingBackfillStatus) error {
	total, err := checkedInt32(status.Total)
	if err != nil {
		return err
	}
	completed, err := checkedInt32(status.Completed)
	if err != nil {
		return err
	}
	failed, err := checkedInt32(status.Failed)
	if err != nil {
		return err
	}
	failures, err := json.Marshal(status.Failures)
	if err != nil {
		return fmt.Errorf("encode knowledge embedding backfill failures: %w", err)
	}
	return r.queries.UpsertKnowledgeEmbeddingBackfill(ctx, dbgen.UpsertKnowledgeEmbeddingBackfillParams{
		WorkspaceID: workspaceID, Status: status.Status, Total: total, Completed: completed, Failed: failed,
		Failures: string(failures), Error: nullableString(status.Error), UpdatedAt: status.UpdatedAt,
	})
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
