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
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sources:     make(map[string]map[string]Source),
		chunks:      make(map[string][]Chunk),
		submissions: make(map[string]map[string]SubmissionRecord),
	}
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
		if previous.PayloadFingerprint != payloadFingerprint {
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
