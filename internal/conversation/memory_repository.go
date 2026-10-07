package conversation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"ariad/internal/usage"
)

// MemoryRepository is a concurrency-safe local conversation repository.
type MemoryRepository struct {
	mu            sync.RWMutex
	turns         map[string]map[string]TurnRecord
	conversations map[string]map[string]string
}

// ListConversations returns summaries for one workspace in descending activity order.
func (r *MemoryRepository) ListConversations(ctx context.Context, workspaceID string) ([]Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	byConversation := make(map[string]Summary)
	latestMessageIDs := make(map[string]string)
	for _, record := range r.turns[workspaceID] {
		message := record.Turn.Message
		summary, found := byConversation[message.ConversationID]
		if !found {
			summary = Summary{ConversationID: message.ConversationID, VisitorID: message.VisitorID}
		}
		summary.MessageCount++
		if !found || message.CreatedAt.After(summary.LastActivityAt) ||
			(message.CreatedAt.Equal(summary.LastActivityAt) && message.ID > latestMessageIDs[message.ConversationID]) {
			summary.LastActivityAt = message.CreatedAt
			summary.LastMessageText = message.Text
			latestMessageIDs[message.ConversationID] = message.ID
		}
		byConversation[message.ConversationID] = summary
	}
	summaries := make([]Summary, 0, len(byConversation))
	for _, summary := range byConversation {
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(left, right int) bool {
		if summaries[left].LastActivityAt.Equal(summaries[right].LastActivityAt) {
			return summaries[left].ConversationID > summaries[right].ConversationID
		}
		return summaries[left].LastActivityAt.After(summaries[right].LastActivityAt)
	})
	return summaries, nil
}

// ListTurns returns the most recent limited set in chronological order. A
// zero limit returns the full conversation.
func (r *MemoryRepository) ListTurns(
	ctx context.Context,
	workspaceID string,
	conversationID string,
	visitorID string,
	limit int,
) ([]Turn, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list turns: %w", err)
	}
	if limit < 0 {
		return nil, errors.New("turn limit cannot be negative")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	turns := make([]Turn, 0)
	for _, record := range r.turns[workspaceID] {
		if record.Turn.Message.ConversationID == conversationID &&
			record.Turn.Message.VisitorID == visitorID {
			turns = append(turns, cloneTurn(record.Turn))
		}
	}
	sort.Slice(turns, func(left, right int) bool {
		if turns[left].Message.CreatedAt.Equal(turns[right].Message.CreatedAt) {
			return turns[left].Message.ID < turns[right].Message.ID
		}
		return turns[left].Message.CreatedAt.Before(turns[right].Message.CreatedAt)
	})
	if limit != AllTurnsLimit && len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	return turns, nil
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
// The usage fact is intentionally discarded: only the PostgreSQL
// implementation persists usage and outbox facts transactionally.
func (r *MemoryRepository) SaveTurn(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	turn Turn,
	_ usage.Fact,
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
