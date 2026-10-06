-- +goose Up

CREATE TABLE knowledge_sources (
    workspace_id text NOT NULL,
    source_id text NOT NULL,
    title text NOT NULL,
    idempotency_key text NOT NULL,
    payload_fingerprint text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, source_id),
    UNIQUE (workspace_id, idempotency_key),
    CONSTRAINT knowledge_sources_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT knowledge_sources_source_id_not_empty CHECK (btrim(source_id) <> ''),
    CONSTRAINT knowledge_sources_title_not_empty CHECK (btrim(title) <> ''),
    CONSTRAINT knowledge_sources_idempotency_key_not_empty CHECK (btrim(idempotency_key) <> ''),
    CONSTRAINT knowledge_sources_payload_fingerprint_not_empty CHECK (btrim(payload_fingerprint) <> '')
);

CREATE TABLE knowledge_chunks (
    workspace_id text NOT NULL,
    chunk_id text NOT NULL,
    source_id text NOT NULL,
    ordinal integer NOT NULL,
    text text NOT NULL,
    PRIMARY KEY (workspace_id, chunk_id),
    UNIQUE (workspace_id, source_id, chunk_id),
    UNIQUE (workspace_id, source_id, ordinal),
    CONSTRAINT knowledge_chunks_source_fk
        FOREIGN KEY (workspace_id, source_id)
        REFERENCES knowledge_sources (workspace_id, source_id),
    CONSTRAINT knowledge_chunks_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT knowledge_chunks_chunk_id_not_empty CHECK (btrim(chunk_id) <> ''),
    CONSTRAINT knowledge_chunks_source_id_not_empty CHECK (btrim(source_id) <> ''),
    CONSTRAINT knowledge_chunks_ordinal_nonnegative CHECK (ordinal >= 0),
    CONSTRAINT knowledge_chunks_text_not_empty CHECK (btrim(text) <> '')
);

CREATE INDEX knowledge_chunks_workspace_source_ordinal_idx
    ON knowledge_chunks (workspace_id, source_id, ordinal);

CREATE TABLE conversation_messages (
    workspace_id text NOT NULL,
    message_id text NOT NULL,
    conversation_id text NOT NULL,
    visitor_id text NOT NULL,
    channel text NOT NULL,
    locale text NOT NULL,
    text text NOT NULL,
    idempotency_key text NOT NULL,
    payload_fingerprint text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, message_id),
    UNIQUE (workspace_id, message_id, conversation_id),
    UNIQUE (workspace_id, idempotency_key),
    CONSTRAINT conversation_messages_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT conversation_messages_message_id_not_empty CHECK (btrim(message_id) <> ''),
    CONSTRAINT conversation_messages_conversation_id_not_empty CHECK (btrim(conversation_id) <> ''),
    CONSTRAINT conversation_messages_visitor_id_not_empty CHECK (btrim(visitor_id) <> ''),
    CONSTRAINT conversation_messages_channel_not_empty CHECK (btrim(channel) <> ''),
    CONSTRAINT conversation_messages_text_not_empty CHECK (btrim(text) <> ''),
    CONSTRAINT conversation_messages_idempotency_key_not_empty CHECK (btrim(idempotency_key) <> ''),
    CONSTRAINT conversation_messages_payload_fingerprint_not_empty CHECK (btrim(payload_fingerprint) <> '')
);

CREATE INDEX conversation_messages_workspace_conversation_visitor_idx
    ON conversation_messages (workspace_id, conversation_id, visitor_id);

CREATE TABLE conversation_answers (
    workspace_id text NOT NULL,
    answer_id text NOT NULL,
    conversation_id text NOT NULL,
    message_id text NOT NULL,
    agent_id text NOT NULL,
    terminal_disposition text NOT NULL,
    text text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, answer_id),
    UNIQUE (workspace_id, message_id),
    CONSTRAINT conversation_answers_message_fk
        FOREIGN KEY (workspace_id, message_id, conversation_id)
        REFERENCES conversation_messages (workspace_id, message_id, conversation_id),
    CONSTRAINT conversation_answers_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT conversation_answers_answer_id_not_empty CHECK (btrim(answer_id) <> ''),
    CONSTRAINT conversation_answers_conversation_id_not_empty CHECK (btrim(conversation_id) <> ''),
    CONSTRAINT conversation_answers_message_id_not_empty CHECK (btrim(message_id) <> ''),
    CONSTRAINT conversation_answers_agent_id_not_empty CHECK (btrim(agent_id) <> ''),
    CONSTRAINT conversation_answers_terminal_disposition_valid CHECK (
        terminal_disposition IN ('answered', 'clarify', 'refused', 'handoff')
    )
);

CREATE INDEX conversation_answers_workspace_conversation_created_idx
    ON conversation_answers (workspace_id, conversation_id, created_at);

CREATE TABLE conversation_citations (
    workspace_id text NOT NULL,
    citation_id text NOT NULL,
    answer_id text NOT NULL,
    evidence_id text NOT NULL,
    source_id text NOT NULL,
    chunk_id text NOT NULL,
    source_title text NOT NULL,
    quote text NOT NULL,
    ordinal integer NOT NULL,
    PRIMARY KEY (workspace_id, citation_id),
    UNIQUE (workspace_id, answer_id, ordinal),
    CONSTRAINT conversation_citations_answer_fk
        FOREIGN KEY (workspace_id, answer_id)
        REFERENCES conversation_answers (workspace_id, answer_id),
    CONSTRAINT conversation_citations_chunk_fk
        FOREIGN KEY (workspace_id, source_id, chunk_id)
        REFERENCES knowledge_chunks (workspace_id, source_id, chunk_id),
    CONSTRAINT conversation_citations_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT conversation_citations_citation_id_not_empty CHECK (btrim(citation_id) <> ''),
    CONSTRAINT conversation_citations_answer_id_not_empty CHECK (btrim(answer_id) <> ''),
    CONSTRAINT conversation_citations_evidence_id_not_empty CHECK (btrim(evidence_id) <> ''),
    CONSTRAINT conversation_citations_source_id_not_empty CHECK (btrim(source_id) <> ''),
    CONSTRAINT conversation_citations_chunk_id_not_empty CHECK (btrim(chunk_id) <> ''),
    CONSTRAINT conversation_citations_ordinal_nonnegative CHECK (ordinal >= 0)
);

CREATE INDEX conversation_citations_workspace_answer_ordinal_idx
    ON conversation_citations (workspace_id, answer_id, ordinal);

CREATE TABLE usage_facts (
    workspace_id text NOT NULL,
    deduplication_key text NOT NULL,
    agent_id text NOT NULL,
    request_id text NOT NULL,
    answer_id text NOT NULL,
    provider text NOT NULL,
    model text NOT NULL,
    input_units bigint NOT NULL,
    cached_input_units bigint NOT NULL,
    output_units bigint NOT NULL,
    status text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, deduplication_key),
    CONSTRAINT usage_facts_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT usage_facts_deduplication_key_not_empty CHECK (btrim(deduplication_key) <> ''),
    CONSTRAINT usage_facts_agent_id_not_empty CHECK (btrim(agent_id) <> ''),
    CONSTRAINT usage_facts_request_id_not_empty CHECK (btrim(request_id) <> ''),
    CONSTRAINT usage_facts_answer_id_not_empty CHECK (btrim(answer_id) <> ''),
    CONSTRAINT usage_facts_units_nonnegative CHECK (
        input_units >= 0 AND cached_input_units >= 0 AND output_units >= 0
    ),
    CONSTRAINT usage_facts_status_valid CHECK (
        status IN ('completed', 'failed', 'not_invoked', 'interrupted', 'unmetered')
    )
);

CREATE INDEX usage_facts_workspace_answer_idx
    ON usage_facts (workspace_id, answer_id);

CREATE INDEX usage_facts_workspace_occurred_idx
    ON usage_facts (workspace_id, occurred_at);

CREATE TABLE outbox (
    workspace_id text NOT NULL,
    event_id text NOT NULL,
    event_type text NOT NULL,
    event_version integer NOT NULL,
    occurred_at timestamptz NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    request_id text NOT NULL,
    causation_id text NOT NULL,
    payload jsonb NOT NULL,
    published_at timestamptz,
    PRIMARY KEY (workspace_id, event_id),
    CONSTRAINT outbox_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT outbox_event_id_not_empty CHECK (btrim(event_id) <> ''),
    CONSTRAINT outbox_event_type_not_empty CHECK (btrim(event_type) <> ''),
    CONSTRAINT outbox_event_version_positive CHECK (event_version > 0),
    CONSTRAINT outbox_aggregate_type_not_empty CHECK (btrim(aggregate_type) <> ''),
    CONSTRAINT outbox_aggregate_id_not_empty CHECK (btrim(aggregate_id) <> ''),
    CONSTRAINT outbox_request_id_not_empty CHECK (btrim(request_id) <> ''),
    CONSTRAINT outbox_causation_id_not_empty CHECK (btrim(causation_id) <> ''),
    CONSTRAINT outbox_payload_is_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX outbox_workspace_unpublished_idx
    ON outbox (workspace_id, occurred_at, event_id)
    WHERE published_at IS NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ariad_reject_immutable_row_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER knowledge_sources_are_immutable
    BEFORE UPDATE OR DELETE ON knowledge_sources
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();

CREATE TRIGGER knowledge_chunks_are_immutable
    BEFORE UPDATE OR DELETE ON knowledge_chunks
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();

CREATE TRIGGER conversation_messages_are_immutable
    BEFORE UPDATE OR DELETE ON conversation_messages
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();

CREATE TRIGGER conversation_answers_are_immutable
    BEFORE UPDATE OR DELETE ON conversation_answers
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();

CREATE TRIGGER conversation_citations_are_immutable
    BEFORE UPDATE OR DELETE ON conversation_citations
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();

CREATE TRIGGER usage_facts_are_immutable
    BEFORE UPDATE OR DELETE ON usage_facts
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ariad_restrict_outbox_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
        OR NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.event_type IS DISTINCT FROM OLD.event_type
        OR NEW.event_version IS DISTINCT FROM OLD.event_version
        OR NEW.occurred_at IS DISTINCT FROM OLD.occurred_at
        OR NEW.aggregate_type IS DISTINCT FROM OLD.aggregate_type
        OR NEW.aggregate_id IS DISTINCT FROM OLD.aggregate_id
        OR NEW.request_id IS DISTINCT FROM OLD.request_id
        OR NEW.causation_id IS DISTINCT FROM OLD.causation_id
        OR NEW.payload IS DISTINCT FROM OLD.payload
        OR OLD.published_at IS NOT NULL THEN
        RAISE EXCEPTION 'outbox event fields are immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER outbox_fields_are_immutable
    BEFORE UPDATE ON outbox
    FOR EACH ROW EXECUTE FUNCTION ariad_restrict_outbox_update();

CREATE TRIGGER outbox_rows_cannot_be_deleted
    BEFORE DELETE ON outbox
    FOR EACH ROW EXECUTE FUNCTION ariad_reject_immutable_row_change();
