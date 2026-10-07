-- +goose Up

CREATE TABLE administrators (
    administrator_id text PRIMARY KEY,
    username text NOT NULL UNIQUE,
    password_hash bytea NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE administrator_sessions (
    token_hash bytea PRIMARY KEY,
    administrator_id text NOT NULL REFERENCES administrators(administrator_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX administrator_sessions_expiry_idx ON administrator_sessions (expires_at);

-- +goose Down

DROP TABLE administrator_sessions;
DROP TABLE administrators;
