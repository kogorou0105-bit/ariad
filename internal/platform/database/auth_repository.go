package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	dbgen "ariad/db/generated"
	"ariad/internal/auth"

	"github.com/jackc/pgx/v5/pgconn"
)

type AuthRepository struct {
	database *sql.DB
	queries  *dbgen.Queries
}

var _ auth.Repository = (*AuthRepository)(nil)

func NewAuthRepository(database *sql.DB) *AuthRepository {
	return &AuthRepository{database: database, queries: dbgen.New(database)}
}
func (r *AuthRepository) CountAdministrators(ctx context.Context) (int64, error) {
	return r.queries.CountAdministrators(ctx)
}
func (r *AuthRepository) CreateAdministrator(ctx context.Context, a auth.Administrator, h []byte) error {
	err := r.queries.CreateAdministrator(ctx, dbgen.CreateAdministratorParams{AdministratorID: a.ID, Username: a.Username, PasswordHash: h, CreatedAt: a.CreatedAt})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return auth.ErrUsernameExists
	}
	return err
}
func (r *AuthRepository) FindAdministratorByUsername(ctx context.Context, u string) (auth.Administrator, []byte, bool, error) {
	row, err := r.queries.FindAdministratorByUsername(ctx, u)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Administrator{}, nil, false, nil
	}
	if err != nil {
		return auth.Administrator{}, nil, false, err
	}
	return auth.Administrator{ID: row.AdministratorID, Username: row.Username, CreatedAt: row.CreatedAt}, row.PasswordHash, true, nil
}
func (r *AuthRepository) FindAdministratorByID(ctx context.Context, id string) (auth.Administrator, bool, error) {
	row, err := r.queries.FindAdministratorByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Administrator{}, false, nil
	}
	if err != nil {
		return auth.Administrator{}, false, err
	}
	return auth.Administrator{ID: row.AdministratorID, Username: row.Username, CreatedAt: row.CreatedAt}, true, nil
}
func (r *AuthRepository) ListAdministrators(ctx context.Context) ([]auth.Administrator, error) {
	rows, err := r.queries.ListAdministrators(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]auth.Administrator, 0, len(rows))
	for _, row := range rows {
		result = append(result, auth.Administrator{ID: row.AdministratorID, Username: row.Username, CreatedAt: row.CreatedAt})
	}
	return result, nil
}
func (r *AuthRepository) UpdatePassword(ctx context.Context, id string, hash []byte) error {
	return r.queries.UpdateAdministratorPassword(ctx, dbgen.UpdateAdministratorPasswordParams{PasswordHash: hash, AdministratorID: id})
}
func (r *AuthRepository) DeleteAdministratorIfNotLast(ctx context.Context, id string) (bool, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "LOCK TABLE administrators IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return false, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM administrators WHERE administrator_id = $1)", id).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, auth.ErrAdministratorNotFound
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM administrators").Scan(&count); err != nil {
		return false, err
	}
	if count <= 1 {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM administrators WHERE administrator_id = $1", id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return rows == 1, nil
}
func (r *AuthRepository) CreateSession(ctx context.Context, s auth.Session) error {
	return r.queries.CreateAdministratorSession(ctx, dbgen.CreateAdministratorSessionParams{TokenHash: s.TokenHash[:], AdministratorID: s.AdministratorID, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt})
}
func (r *AuthRepository) FindSession(ctx context.Context, h [sha256.Size]byte, now time.Time) (auth.Session, bool, error) {
	row, err := r.queries.FindValidAdministratorSession(ctx, dbgen.FindValidAdministratorSessionParams{TokenHash: h[:], Now: now})
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, false, nil
	}
	if err != nil {
		return auth.Session{}, false, err
	}
	var hash [sha256.Size]byte
	copy(hash[:], row.TokenHash)
	return auth.Session{TokenHash: hash, AdministratorID: row.AdministratorID, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}, true, nil
}
func (r *AuthRepository) DeleteSession(ctx context.Context, h [sha256.Size]byte) error {
	return r.queries.DeleteAdministratorSession(ctx, h[:])
}
func (r *AuthRepository) DeleteAdministratorSessions(ctx context.Context, id string) error {
	return r.queries.DeleteAdministratorSessions(ctx, id)
}
func (r *AuthRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return r.queries.DeleteExpiredAdministratorSessions(ctx, now)
}
