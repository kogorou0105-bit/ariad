package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	dbgen "ariad/db/generated"
	"ariad/internal/review"
)

// ReviewRepository persists the workspace-scoped human handoff queue.
type ReviewRepository struct {
	database *sql.DB
	queries  *dbgen.Queries
}

var _ review.Repository = (*ReviewRepository)(nil)

func NewReviewRepository(database *sql.DB) *ReviewRepository {
	return &ReviewRepository{database: database, queries: dbgen.New(database)}
}

func (r *ReviewRepository) ConversationOwned(ctx context.Context, workspaceID, conversationID, visitorID string) (bool, error) {
	return r.queries.ConversationExists(ctx, dbgen.ConversationExistsParams{WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID})
}

func (r *ReviewRepository) MarkPending(ctx context.Context, state review.State) error {
	return r.queries.MarkConversationPending(ctx, dbgen.MarkConversationPendingParams{
		WorkspaceID: state.WorkspaceID, ConversationID: state.ConversationID, VisitorID: state.VisitorID,
		HandoffReason: state.Reason, RequestedBy: state.RequestedBy,
		RequestedAt: sql.NullTime{Time: state.RequestedAt, Valid: true},
	})
}

func (r *ReviewRepository) GetState(ctx context.Context, workspaceID, conversationID, visitorID string) (review.State, bool, error) {
	row, err := r.queries.GetConversationState(ctx, dbgen.GetConversationStateParams{WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID})
	if errors.Is(err, sql.ErrNoRows) {
		return review.State{}, false, nil
	}
	if err != nil {
		return review.State{}, false, fmt.Errorf("get conversation state: %w", err)
	}
	return stateFromRow(row), true, nil
}

func (r *ReviewRepository) ListPending(ctx context.Context, workspaceID string) ([]review.QueueItem, error) {
	rows, err := r.queries.ListPendingReviews(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list pending reviews: %w", err)
	}
	items := make([]review.QueueItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, review.QueueItem{ConversationID: row.ConversationID, VisitorID: row.VisitorID, Reason: row.HandoffReason, RequestedBy: row.HandoffRequestedBy, RequestedAt: row.HandoffRequestedAt.Time, LastActivityAt: row.UpdatedAt, LastMessageText: row.LastMessageText})
	}
	return items, nil
}

func (r *ReviewRepository) SaveReply(ctx context.Context, workspaceID string, reply review.HumanReply) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := r.queries.WithTx(tx)
	inserted, err := q.InsertHumanReply(ctx, dbgen.InsertHumanReplyParams{WorkspaceID: workspaceID, ReplyID: reply.ID, ConversationID: reply.ConversationID, VisitorID: reply.VisitorID, AuthorID: reply.AuthorID, Text: reply.Text, CreatedAt: reply.CreatedAt})
	if err != nil {
		return err
	}
	if inserted != 1 {
		return review.ErrNotPending
	}
	if err := q.TouchConversationState(ctx, dbgen.TouchConversationStateParams{UpdatedAt: reply.CreatedAt, WorkspaceID: workspaceID, ConversationID: reply.ConversationID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ReviewRepository) ListReplies(ctx context.Context, workspaceID, conversationID, visitorID string) ([]review.HumanReply, error) {
	rows, err := r.queries.ListHumanReplies(ctx, dbgen.ListHumanRepliesParams{WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID})
	if err != nil {
		return nil, err
	}
	replies := make([]review.HumanReply, 0, len(rows))
	for _, row := range rows {
		replies = append(replies, review.HumanReply{ID: row.ReplyID, ConversationID: row.ConversationID, VisitorID: row.VisitorID, AuthorID: row.AuthorID, Text: row.Text, CreatedAt: row.CreatedAt})
	}
	return replies, nil
}

func (r *ReviewRepository) Resolve(ctx context.Context, workspaceID, conversationID, resolvedBy string, resolvedAt time.Time) (bool, error) {
	rows, err := r.queries.ResolveConversation(ctx, dbgen.ResolveConversationParams{ResolvedBy: resolvedBy, ResolvedAt: sql.NullTime{Time: resolvedAt, Valid: true}, WorkspaceID: workspaceID, ConversationID: conversationID})
	return rows == 1, err
}

func (r *ReviewRepository) ReopenResolved(ctx context.Context, workspaceID, conversationID, visitorID string, updatedAt time.Time) error {
	return r.queries.ReopenResolvedConversation(ctx, dbgen.ReopenResolvedConversationParams{UpdatedAt: updatedAt, WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID})
}

func stateFromRow(row dbgen.ConversationState) review.State {
	state := review.State{WorkspaceID: row.WorkspaceID, ConversationID: row.ConversationID, VisitorID: row.VisitorID, Status: review.Status(row.Status), Reason: row.HandoffReason, RequestedBy: row.HandoffRequestedBy, RequestedAt: row.HandoffRequestedAt.Time, ResolvedBy: row.ResolvedBy, UpdatedAt: row.UpdatedAt}
	if row.ResolvedAt.Valid {
		at := row.ResolvedAt.Time
		state.ResolvedAt = &at
	}
	return state
}
