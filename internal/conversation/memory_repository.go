package conversation

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// MemoryRepository is a concurrency-safe local conversation repository.
type MemoryRepository struct {
	mu            sync.RWMutex
	turns         map[string]map[string]TurnRecord
	conversations map[string]map[string]string
}

// NewMemoryRepository creates an empty local conversation repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		turns:         make(map[string]map[string]TurnRecord),
		conversations: make(map[string]map[string]string),
	}
}

// FindTurnByIdempotencyKey finds a turn inside one workspace.
func (r *MemoryRepository) FindTurnByIdempotencyKey(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (TurnRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return TurnRecord{}, false, fmt.Errorf("find turn: %w", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, found := r.turns[workspaceID][idempotencyKey]
	record.Turn = cloneTurn(record.Turn)
	return record, found, nil
}

// ConversationExists uses a workspace-local ownership index instead of scanning turns.
func (r *MemoryRepository) ConversationExists(
	ctx context.Context,
	workspaceID string,
	conversationID string,
	visitorID string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("find conversation: %w", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner, found := r.conversations[workspaceID][conversationID]
	return found && owner == visitorID, nil
}

// SaveTurn atomically stores one turn and its idempotency key in memory.
func (r *MemoryRepository) SaveTurn(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	turn Turn,
) (Turn, error) {
	if err := ctx.Err(); err != nil {
		return Turn{}, fmt.Errorf("save turn: %w", err)
	}
	if turn.Message.WorkspaceID != workspaceID || turn.Answer.WorkspaceID != workspaceID {
		return Turn{}, errors.New("turn workspace does not match lookup workspace")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.turns[workspaceID]; !ok {
		r.turns[workspaceID] = make(map[string]TurnRecord)
	}
	if _, ok := r.conversations[workspaceID]; !ok {
		r.conversations[workspaceID] = make(map[string]string)
	}
	if previous, found := r.turns[workspaceID][idempotencyKey]; found {
		if previous.PayloadFingerprint != payloadFingerprint {
			return Turn{}, ErrIdempotencyConflict
		}
		return cloneTurn(previous.Turn), nil
	}
	conversationID := turn.Message.ConversationID
	if owner, found := r.conversations[workspaceID][conversationID]; found && owner != turn.Message.VisitorID {
		return Turn{}, ErrConversationNotFound
	}
	r.conversations[workspaceID][conversationID] = turn.Message.VisitorID
	r.turns[workspaceID][idempotencyKey] = TurnRecord{
		Turn:               cloneTurn(turn),
		PayloadFingerprint: payloadFingerprint,
	}
	return cloneTurn(turn), nil
}

func cloneTurn(turn Turn) Turn {
	turn.Answer.Citations = append([]Citation(nil), turn.Answer.Citations...)
	return turn
}
