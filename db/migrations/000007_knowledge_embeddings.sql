-- +goose Up
CREATE TABLE knowledge_chunk_embeddings (
    workspace_id text NOT NULL,
    chunk_id text NOT NULL,
    embedding text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, chunk_id),
    FOREIGN KEY (workspace_id, chunk_id) REFERENCES knowledge_chunks (workspace_id, chunk_id) ON DELETE CASCADE
);
CREATE TABLE knowledge_embedding_backfills (
    workspace_id text PRIMARY KEY,
    status text NOT NULL,
    total integer NOT NULL DEFAULT 0,
    completed integer NOT NULL DEFAULT 0,
    failed integer NOT NULL DEFAULT 0,
    failures text NOT NULL DEFAULT '[]',
    error text,
    updated_at timestamptz NOT NULL,
    CONSTRAINT knowledge_embedding_backfills_status CHECK (status IN ('running', 'completed', 'failed'))
);
ALTER TABLE workspace_model_configs
    ADD COLUMN embedding_base_url text,
    ADD COLUMN embedding_model text,
    ADD COLUMN embedding_api_key_ciphertext text,
    ADD COLUMN embedding_threshold double precision NOT NULL DEFAULT 0.35;
-- +goose Down
ALTER TABLE workspace_model_configs DROP COLUMN embedding_threshold, DROP COLUMN embedding_api_key_ciphertext, DROP COLUMN embedding_model, DROP COLUMN embedding_base_url;
DROP TABLE knowledge_embedding_backfills;
DROP TABLE knowledge_chunk_embeddings;
