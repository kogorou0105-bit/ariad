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
// error. It checks core tables without taking a dependency on goose.
func VerifySchemaReady(ctx context.Context, database *sql.DB) error {
	var conversations, conversationStates, administrators *string
	if err := database.QueryRowContext(
		ctx,
		"SELECT to_regclass('conversation_messages'), to_regclass('conversation_states'), to_regclass('administrators')",
	).Scan(&conversations, &conversationStates, &administrators); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if conversations == nil || *conversations == "" || conversationStates == nil || *conversationStates == "" || administrators == nil || *administrators == "" {
		return errors.New("database schema is not migrated; run 'make migrate-up' first")
	}
	return nil
}
