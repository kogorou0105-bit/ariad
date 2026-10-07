-- +goose Up

DROP TRIGGER knowledge_sources_are_immutable ON knowledge_sources;
DROP TRIGGER knowledge_chunks_are_immutable ON knowledge_chunks;

ALTER TABLE knowledge_sources
    ADD COLUMN source_type text NOT NULL DEFAULT 'text',
    ADD COLUMN status text NOT NULL DEFAULT 'ready',
    ADD COLUMN file_name text,
    ADD COLUMN media_type text,
    ADD COLUMN file_size bigint,
    ADD COLUMN file_content bytea,
    ADD COLUMN error_message text,
    ADD COLUMN updated_at timestamptz;

UPDATE knowledge_sources
SET source_type = CASE WHEN source_url IS NULL THEN 'text' ELSE 'url' END,
    updated_at = created_at;

ALTER TABLE knowledge_sources ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE knowledge_sources
    ADD CONSTRAINT knowledge_sources_type_valid CHECK (source_type IN ('text', 'url', 'file')),
    ADD CONSTRAINT knowledge_sources_status_valid CHECK (status IN ('processing', 'ready', 'failed')),
    ADD CONSTRAINT knowledge_sources_file_size_valid CHECK (file_size IS NULL OR file_size >= 0);

ALTER TABLE knowledge_chunks DROP CONSTRAINT knowledge_chunks_source_fk;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_source_fk
    FOREIGN KEY (workspace_id, source_id)
    REFERENCES knowledge_sources (workspace_id, source_id) ON DELETE CASCADE;
ALTER TABLE conversation_citations DROP CONSTRAINT conversation_citations_chunk_fk;
ALTER TABLE conversation_citations ADD CONSTRAINT conversation_citations_chunk_fk
    FOREIGN KEY (workspace_id, source_id, chunk_id)
    REFERENCES knowledge_chunks (workspace_id, source_id, chunk_id) ON DELETE CASCADE;

-- Sources and chunks are still only mutated through the knowledge service. The
-- old blanket triggers prevented the required delete/reprocess lifecycle.

-- +goose Down

DELETE FROM knowledge_sources WHERE source_type = 'file';
ALTER TABLE conversation_citations DROP CONSTRAINT conversation_citations_chunk_fk;
ALTER TABLE conversation_citations ADD CONSTRAINT conversation_citations_chunk_fk
    FOREIGN KEY (workspace_id, source_id, chunk_id)
    REFERENCES knowledge_chunks (workspace_id, source_id, chunk_id);
ALTER TABLE knowledge_chunks DROP CONSTRAINT knowledge_chunks_source_fk;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_source_fk
    FOREIGN KEY (workspace_id, source_id)
    REFERENCES knowledge_sources (workspace_id, source_id);
ALTER TABLE knowledge_sources
    DROP CONSTRAINT knowledge_sources_file_size_valid,
    DROP CONSTRAINT knowledge_sources_status_valid,
    DROP CONSTRAINT knowledge_sources_type_valid,
    DROP COLUMN updated_at,
    DROP COLUMN error_message,
    DROP COLUMN file_content,
    DROP COLUMN file_size,
    DROP COLUMN media_type,
    DROP COLUMN file_name,
    DROP COLUMN status,
    DROP COLUMN source_type;
CREATE TRIGGER knowledge_sources_are_immutable BEFORE UPDATE OR DELETE ON knowledge_sources
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();
CREATE TRIGGER knowledge_chunks_are_immutable BEFORE UPDATE OR DELETE ON knowledge_chunks
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();
