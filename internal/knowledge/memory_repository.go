package knowledge

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// MemoryRepository is a concurrency-safe local knowledge repository.
type MemoryRepository struct {
	mu          sync.RWMutex
	sources     map[string]map[string]Source
	chunks      map[string][]Chunk
	submissions map[string]map[string]SubmissionRecord
	backfills   map[string]EmbeddingBackfillStatus
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sources:     make(map[string]map[string]Source),
		chunks:      make(map[string][]Chunk),
		submissions: make(map[string]map[string]SubmissionRecord),
		backfills:   make(map[string]EmbeddingBackfillStatus),
	}
}

func (r *MemoryRepository) GetEmbeddingBackfill(ctx context.Context, workspaceID string) (EmbeddingBackfillStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return EmbeddingBackfillStatus{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	status, found := r.backfills[workspaceID]
	status.Failures = append([]EmbeddingFailure(nil), status.Failures...)
	return status, found, nil
}

func (r *MemoryRepository) SaveEmbeddingBackfill(ctx context.Context, workspaceID string, status EmbeddingBackfillStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status.Failures = append([]EmbeddingFailure(nil), status.Failures...)
	r.mu.Lock()
	r.backfills[workspaceID] = status
	r.mu.Unlock()
	return nil
}

// FindSubmission finds an idempotent submission inside one workspace.
func (r *MemoryRepository) FindSubmission(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (SubmissionRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return SubmissionRecord{}, false, fmt.Errorf("find submission: %w", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result, found := r.submissions[workspaceID][idempotencyKey]
	return result, found, nil
}

// SaveSource atomically stores a source's chunks and idempotency result in memory.
func (r *MemoryRepository) SaveSource(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	source Source,
	chunks []Chunk,
) (SubmitTextResult, error) {
	if err := ctx.Err(); err != nil {
		return SubmitTextResult{}, fmt.Errorf("save source: %w", err)
	}
	if source.WorkspaceID != workspaceID {
		return SubmitTextResult{}, errors.New("source workspace does not match lookup workspace")
	}
	for _, chunk := range chunks {
		if chunk.WorkspaceID != workspaceID || chunk.SourceID != source.ID {
			return SubmitTextResult{}, errors.New("chunk scope does not match source scope")
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, found := r.submissions[workspaceID][idempotencyKey]; found {
		if previous.PayloadFingerprint != payloadFingerprint || previous.SourceURL != source.SourceURL {
			return SubmitTextResult{}, ErrIdempotencyConflict
		}
		return previous.Result, nil
	}
	if _, ok := r.submissions[workspaceID]; !ok {
		r.submissions[workspaceID] = make(map[string]SubmissionRecord)
	}
	if _, ok := r.sources[workspaceID]; !ok {
		r.sources[workspaceID] = make(map[string]Source)
	}
	if _, exists := r.sources[workspaceID][source.ID]; exists {
		return SubmitTextResult{}, errors.New("source identifier already exists")
	}
	r.sources[workspaceID][source.ID] = source
	r.chunks[workspaceID] = append(r.chunks[workspaceID], chunks...)
	result := SubmitTextResult{
		WorkspaceID: workspaceID,
		SourceID:    source.ID,
		ChunkCount:  len(chunks),
	}
	r.submissions[workspaceID][idempotencyKey] = SubmissionRecord{
		Result:             result,
		SourceTitle:        source.Title,
		SourceURL:          source.SourceURL,
		PayloadFingerprint: payloadFingerprint,
	}
	return result, nil
}

// ListChunks returns copied chunks and never exposes mutable repository state.
func (r *MemoryRepository) ListChunks(
	ctx context.Context,
	workspaceID string,
) ([]Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Chunk(nil), r.chunks[workspaceID]...), nil
}

func (r *MemoryRepository) SaveEmbedding(ctx context.Context, workspaceID, chunkID string, embedding []float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.chunks[workspaceID] {
		if r.chunks[workspaceID][index].ID == chunkID {
			r.chunks[workspaceID][index].Embedding = append([]float64(nil), embedding...)
			return nil
		}
	}
	return errors.New("knowledge chunk not found")
}

func (r *MemoryRepository) ListSources(ctx context.Context, workspaceID string) ([]SourceSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]SourceSummary, 0, len(r.sources[workspaceID]))
	for _, source := range r.sources[workspaceID] {
		count := 0
		for _, chunk := range r.chunks[workspaceID] {
			if chunk.SourceID == source.ID {
				count++
			}
		}
		source.FileContent = nil
		result = append(result, SourceSummary{Source: source, ChunkCount: count})
	}
	return result, nil
}

func (r *MemoryRepository) GetSource(ctx context.Context, workspaceID, sourceID string) (SourceSummary, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceSummary{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	source, found := r.sources[workspaceID][sourceID]
	if !found {
		return SourceSummary{}, false, nil
	}
	count := 0
	for _, chunk := range r.chunks[workspaceID] {
		if chunk.SourceID == sourceID {
			count++
		}
	}
	source.FileContent = append([]byte(nil), source.FileContent...)
	return SourceSummary{Source: source, ChunkCount: count}, true, nil
}

func (r *MemoryRepository) DeleteSource(ctx context.Context, workspaceID, sourceID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.sources[workspaceID][sourceID]; !found {
		return false, nil
	}
	delete(r.sources[workspaceID], sourceID)
	filtered := r.chunks[workspaceID][:0]
	for _, chunk := range r.chunks[workspaceID] {
		if chunk.SourceID != sourceID {
			filtered = append(filtered, chunk)
		}
	}
	r.chunks[workspaceID] = filtered
	for key, record := range r.submissions[workspaceID] {
		if record.Result.SourceID == sourceID {
			delete(r.submissions[workspaceID], key)
		}
	}
	return true, nil
}

func (r *MemoryRepository) ReplaceSourceChunks(ctx context.Context, source Source, chunks []Chunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.sources[source.WorkspaceID][source.ID]; !found {
		return errors.New("knowledge source not found")
	}
	filtered := r.chunks[source.WorkspaceID][:0]
	for _, chunk := range r.chunks[source.WorkspaceID] {
		if chunk.SourceID != source.ID {
			filtered = append(filtered, chunk)
		}
	}
	r.chunks[source.WorkspaceID] = append(filtered, chunks...)
	r.sources[source.WorkspaceID][source.ID] = source
	return nil
}
