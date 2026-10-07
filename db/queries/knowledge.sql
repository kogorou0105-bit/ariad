-- name: GetKnowledgeSubmission :one
SELECT
    source.source_id,
    source.title,
    source.source_url,
    source.payload_fingerprint,
    count(chunk.chunk_id)::bigint AS chunk_count
FROM knowledge_sources AS source
LEFT JOIN knowledge_chunks AS chunk
    ON chunk.workspace_id = source.workspace_id
    AND chunk.source_id = source.source_id
WHERE source.workspace_id = sqlc.arg(workspace_id)
  AND source.idempotency_key = sqlc.arg(idempotency_key)
GROUP BY source.source_id, source.title, source.source_url, source.payload_fingerprint;

-- name: LockKnowledgeSubmission :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'knowledge-submission:' || sqlc.arg(workspace_id) || ':' || sqlc.arg(idempotency_key),
    0
));

-- name: InsertKnowledgeSource :one
INSERT INTO knowledge_sources (
    workspace_id,
    source_id,
    title,
    source_url,
    idempotency_key,
    payload_fingerprint,
    created_at
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(source_id),
    sqlc.arg(title),
    sqlc.narg(source_url),
    sqlc.arg(idempotency_key),
    sqlc.arg(payload_fingerprint),
    sqlc.arg(created_at)
)
RETURNING workspace_id, source_id, title, source_url, idempotency_key, payload_fingerprint, created_at;

-- name: InsertKnowledgeChunk :exec
INSERT INTO knowledge_chunks (
    workspace_id,
    chunk_id,
    source_id,
    ordinal,
    text
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(chunk_id),
    sqlc.arg(source_id),
    sqlc.arg(ordinal),
    sqlc.arg(text)
);

-- name: UpsertKnowledgeChunkEmbedding :exec
INSERT INTO knowledge_chunk_embeddings (workspace_id, chunk_id, embedding, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(chunk_id), sqlc.arg(embedding), sqlc.arg(updated_at))
ON CONFLICT (workspace_id, chunk_id) DO UPDATE SET embedding = EXCLUDED.embedding, updated_at = EXCLUDED.updated_at;

-- name: ListKnowledgeChunks :many
SELECT
    chunk.chunk_id,
    chunk.workspace_id,
    chunk.source_id,
    source.title AS source_title,
    chunk.ordinal,
    chunk.text,
    embedding.embedding
FROM knowledge_chunks AS chunk
JOIN knowledge_sources AS source
    ON source.workspace_id = chunk.workspace_id
    AND source.source_id = chunk.source_id
LEFT JOIN knowledge_chunk_embeddings AS embedding ON embedding.workspace_id = chunk.workspace_id AND embedding.chunk_id = chunk.chunk_id
WHERE chunk.workspace_id = sqlc.arg(workspace_id)
ORDER BY source.created_at, chunk.source_id, chunk.ordinal;

-- name: GetKnowledgeEmbeddingBackfill :one
SELECT workspace_id, status, total, completed, failed, failures, error, updated_at
FROM knowledge_embedding_backfills
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertKnowledgeEmbeddingBackfill :exec
INSERT INTO knowledge_embedding_backfills (workspace_id, status, total, completed, failed, failures, error, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(status), sqlc.arg(total), sqlc.arg(completed), sqlc.arg(failed), sqlc.arg(failures), sqlc.narg(error), sqlc.arg(updated_at))
ON CONFLICT (workspace_id) DO UPDATE SET
    status = EXCLUDED.status,
    total = EXCLUDED.total,
    completed = EXCLUDED.completed,
    failed = EXCLUDED.failed,
    failures = EXCLUDED.failures,
    error = EXCLUDED.error,
    updated_at = EXCLUDED.updated_at;
