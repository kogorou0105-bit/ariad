-- +goose Up

CREATE TABLE conversation_states (
    workspace_id text NOT NULL,
    conversation_id text NOT NULL,
    visitor_id text NOT NULL,
    status text NOT NULL,
    handoff_reason text NOT NULL DEFAULT '',
    handoff_requested_by text NOT NULL DEFAULT '',
    handoff_requested_at timestamptz,
    resolved_by text NOT NULL DEFAULT '',
    resolved_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, conversation_id),
    CONSTRAINT conversation_states_status_valid CHECK (status IN ('ongoing', 'pending', 'resolved'))
);

CREATE INDEX conversation_states_pending_idx
    ON conversation_states (workspace_id, updated_at DESC, conversation_id)
    WHERE status = 'pending';

CREATE TABLE conversation_human_replies (
    workspace_id text NOT NULL,
    reply_id text NOT NULL,
    conversation_id text NOT NULL,
    visitor_id text NOT NULL,
    author_id text NOT NULL,
    text text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, reply_id),
    CONSTRAINT conversation_human_replies_text_not_empty CHECK (btrim(text) <> '')
);

CREATE INDEX conversation_human_replies_conversation_idx
    ON conversation_human_replies (workspace_id, conversation_id, visitor_id, created_at, reply_id);

-- +goose Down

DROP TABLE conversation_human_replies;
DROP TABLE conversation_states;
