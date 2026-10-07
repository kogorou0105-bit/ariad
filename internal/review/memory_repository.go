package review

import (
	"context"
	"sort"
	"sync"
	"time"

	"ariad/internal/conversation"
)

type OwnershipChecker interface {
	ConversationExists(context.Context, string, string, string) (bool, error)
	ListConversations(context.Context, string) ([]conversation.Summary, error)
}

type MemoryRepository struct {
	mu        sync.RWMutex
	ownership OwnershipChecker
	states    map[string]map[string]State
	replies   map[string]map[string][]HumanReply
}

func NewMemoryRepository(ownership OwnershipChecker) *MemoryRepository {
	return &MemoryRepository{ownership: ownership, states: make(map[string]map[string]State), replies: make(map[string]map[string][]HumanReply)}
}

func (r *MemoryRepository) ConversationOwned(ctx context.Context, w, c, v string) (bool, error) {
	return r.ownership.ConversationExists(ctx, w, c, v)
}
func (r *MemoryRepository) MarkPending(ctx context.Context, state State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.states[state.WorkspaceID] == nil {
		r.states[state.WorkspaceID] = make(map[string]State)
	}
	r.states[state.WorkspaceID][state.ConversationID] = state
	return nil
}
func (r *MemoryRepository) GetState(ctx context.Context, w, c, v string) (State, bool, error) {
	if err := ctx.Err(); err != nil {
		return State{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.states[w][c]
	return s, ok && s.VisitorID == v, nil
}
func (r *MemoryRepository) ListPending(ctx context.Context, w string) ([]QueueItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	summaries, err := r.ownership.ListConversations(ctx, w)
	if err != nil {
		return nil, err
	}
	previews := make(map[string]string, len(summaries))
	for _, summary := range summaries {
		previews[summary.ConversationID] = summary.LastMessageText
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []QueueItem{}
	for _, s := range r.states[w] {
		if s.Status == StatusPending {
			items = append(items, QueueItem{ConversationID: s.ConversationID, VisitorID: s.VisitorID, Reason: s.Reason, RequestedBy: s.RequestedBy, RequestedAt: s.RequestedAt, LastActivityAt: s.UpdatedAt, LastMessageText: previews[s.ConversationID]})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastActivityAt.After(items[j].LastActivityAt) })
	return items, nil
}
func (r *MemoryRepository) SaveReply(ctx context.Context, w string, reply HumanReply) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.states[w][reply.ConversationID]
	if !ok || s.Status != StatusPending || s.VisitorID != reply.VisitorID {
		return ErrNotPending
	}
	if r.replies[w] == nil {
		r.replies[w] = make(map[string][]HumanReply)
	}
	r.replies[w][reply.ConversationID] = append(r.replies[w][reply.ConversationID], reply)
	s.UpdatedAt = reply.CreatedAt
	r.states[w][reply.ConversationID] = s
	return nil
}
func (r *MemoryRepository) ListReplies(ctx context.Context, w, c, v string) ([]HumanReply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []HumanReply{}
	for _, reply := range r.replies[w][c] {
		if reply.VisitorID == v {
			out = append(out, reply)
		}
	}
	return out, nil
}
func (r *MemoryRepository) Resolve(ctx context.Context, w, c, by string, at time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.states[w][c]
	if !ok || s.Status != StatusPending {
		return false, nil
	}
	s.Status = StatusResolved
	s.ResolvedBy = by
	s.ResolvedAt = &at
	s.UpdatedAt = at
	r.states[w][c] = s
	return true, nil
}

func (r *MemoryRepository) ReopenResolved(ctx context.Context, w, c, v string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.states[w][c]
	if !ok || s.VisitorID != v || s.Status != StatusResolved {
		return nil
	}
	s.Status, s.ResolvedBy, s.ResolvedAt, s.UpdatedAt = StatusOngoing, "", nil, at
	r.states[w][c] = s
	return nil
}
