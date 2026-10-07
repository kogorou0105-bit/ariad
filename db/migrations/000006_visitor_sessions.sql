-- +goose Up
CREATE TABLE visitors (
    workspace_id text NOT NULL,
    visitor_id text NOT NULL,
    refresh_token_hash bytea UNIQUE,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, visitor_id)
);

INSERT INTO visitors (workspace_id, visitor_id, first_seen_at, last_seen_at)
SELECT workspace_id, visitor_id, min(created_at), max(created_at)
FROM conversation_messages GROUP BY workspace_id, visitor_id
ON CONFLICT DO NOTHING;

CREATE TABLE visitor_sessions (
    token_hash bytea PRIMARY KEY,
    workspace_id text NOT NULL,
    visitor_id text NOT NULL,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    FOREIGN KEY (workspace_id, visitor_id) REFERENCES visitors (workspace_id, visitor_id) ON DELETE CASCADE
);

CREATE INDEX visitor_sessions_visitor_idx ON visitor_sessions (workspace_id, visitor_id, last_seen_at DESC);
CREATE INDEX visitor_sessions_expiry_idx ON visitor_sessions (expires_at);

-- +goose Down
DROP TABLE visitor_sessions;
DROP TABLE visitors;
