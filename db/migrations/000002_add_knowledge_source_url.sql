-- +goose Up

ALTER TABLE knowledge_sources
    ADD COLUMN source_url text;

ALTER TABLE knowledge_sources
    ADD CONSTRAINT knowledge_sources_source_url_not_empty
    CHECK (source_url IS NULL OR btrim(source_url) <> '');

-- +goose Down

ALTER TABLE knowledge_sources
    DROP CONSTRAINT knowledge_sources_source_url_not_empty;

ALTER TABLE knowledge_sources
    DROP COLUMN source_url;
