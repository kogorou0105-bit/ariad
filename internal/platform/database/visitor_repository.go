package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	dbgen "ariad/db/generated"
	"ariad/internal/visitor"
)

type VisitorRepository struct {
	database *sql.DB
	queries  *dbgen.Queries
}

var _ visitor.Repository = (*VisitorRepository)(nil)

func NewVisitorRepository(database *sql.DB) *VisitorRepository {
	return &VisitorRepository{database: database, queries: dbgen.New(database)}
}

func (r *VisitorRepository) CreateSession(ctx context.Context, session visitor.Session) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	queries := r.queries.WithTx(tx)
	if err = queries.CreateVisitor(ctx, dbgen.CreateVisitorParams{WorkspaceID: session.Identity.WorkspaceID, VisitorID: session.Identity.VisitorID, RefreshTokenHash: session.Identity.RefreshHash, FirstSeenAt: session.Identity.FirstSeenAt, LastSeenAt: session.LastSeenAt}); err != nil {
		return fmt.Errorf("insert visitor: %w", err)
	}
	if err = queries.CreateVisitorSession(ctx, dbgen.CreateVisitorSessionParams{TokenHash: session.TokenHash, WorkspaceID: session.Identity.WorkspaceID, VisitorID: session.Identity.VisitorID, CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt, ExpiresAt: session.ExpiresAt}); err != nil {
		return fmt.Errorf("insert visitor session: %w", err)
	}
	return tx.Commit()
}

func (r *VisitorRepository) RotateSession(ctx context.Context, oldRefreshHash, newRefreshHash, tokenHash []byte, now, expiresAt time.Time) (visitor.Session, bool, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return visitor.Session{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var identity visitor.Identity
	err = tx.QueryRowContext(ctx, `SELECT workspace_id, visitor_id, first_seen_at FROM visitors WHERE refresh_token_hash = $1 FOR UPDATE`, oldRefreshHash).Scan(&identity.WorkspaceID, &identity.VisitorID, &identity.FirstSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return visitor.Session{}, false, nil
	}
	if err != nil {
		return visitor.Session{}, false, err
	}
	identity.RefreshHash = append([]byte(nil), newRefreshHash...)
	if _, err = tx.ExecContext(ctx, `UPDATE visitors SET refresh_token_hash = $1, last_seen_at = $2 WHERE workspace_id = $3 AND visitor_id = $4`, newRefreshHash, now, identity.WorkspaceID, identity.VisitorID); err != nil {
		return visitor.Session{}, false, err
	}
	queries := r.queries.WithTx(tx)
	session := visitor.Session{TokenHash: append([]byte(nil), tokenHash...), Identity: identity, CreatedAt: now, LastSeenAt: now, ExpiresAt: expiresAt}
	if err = queries.CreateVisitorSession(ctx, dbgen.CreateVisitorSessionParams{TokenHash: tokenHash, WorkspaceID: identity.WorkspaceID, VisitorID: identity.VisitorID, CreatedAt: now, LastSeenAt: now, ExpiresAt: expiresAt}); err != nil {
		return visitor.Session{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return visitor.Session{}, false, err
	}
	return session, true, nil
}

func (r *VisitorRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return r.queries.DeleteExpiredVisitorSessions(ctx, now)
}

func (r *VisitorRepository) FindSession(ctx context.Context, hash []byte) (visitor.Session, bool, error) {
	row, err := r.queries.GetVisitorSession(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return visitor.Session{}, false, nil
	}
	if err != nil {
		return visitor.Session{}, false, err
	}
	return visitor.Session{TokenHash: row.TokenHash, Identity: visitor.Identity{WorkspaceID: row.WorkspaceID, VisitorID: row.VisitorID, FirstSeenAt: row.FirstSeenAt}, CreatedAt: row.CreatedAt, LastSeenAt: row.LastSeenAt, ExpiresAt: row.ExpiresAt}, true, nil
}

func (r *VisitorRepository) TouchSession(ctx context.Context, hash []byte, seenAt, expiresAt time.Time) error {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	queries := r.queries.WithTx(tx)
	row, err := queries.GetVisitorSession(ctx, hash)
	if err != nil {
		return err
	}
	if err = queries.TouchVisitorSession(ctx, dbgen.TouchVisitorSessionParams{TokenHash: hash, LastSeenAt: seenAt, ExpiresAt: expiresAt}); err != nil {
		return err
	}
	if err = queries.TouchVisitor(ctx, dbgen.TouchVisitorParams{WorkspaceID: row.WorkspaceID, VisitorID: row.VisitorID, LastSeenAt: seenAt}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *VisitorRepository) ListProfiles(ctx context.Context, workspaceID string) ([]visitor.Profile, error) {
	rows, err := r.queries.ListVisitorProfiles(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]visitor.Profile, 0, len(rows))
	for _, row := range rows {
		result = append(result, visitor.Profile{VisitorID: row.VisitorID, FirstSeenAt: row.FirstSeenAt, LastActivityAt: row.LastActivityAt})
	}
	return result, nil
}
