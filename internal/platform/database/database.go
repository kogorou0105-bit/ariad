package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	maximumOpenConnections = 10
	maximumIdleConnections = 5
	connectionMaxLifetime  = 30 * time.Minute
)

// Open connects to PostgreSQL and verifies the connection before returning.
func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	database.SetMaxOpenConns(maximumOpenConnections)
	database.SetMaxIdleConns(maximumIdleConnections)
	database.SetConnMaxLifetime(connectionMaxLifetime)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return database, nil
}

// VerifySchemaReady fails fast when the configured database has not had its
// migrations applied, instead of letting the first request return a runtime
// error. It checks for one core table so it does not need the goose library.
func VerifySchemaReady(ctx context.Context, database *sql.DB) error {
	var table *string
	if err := database.QueryRowContext(
		ctx,
		"SELECT to_regclass('conversation_messages')",
	).Scan(&table); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if table == nil || *table == "" {
		return errors.New("database schema is not migrated; run 'make migrate-up' first")
	}
	return nil
}
