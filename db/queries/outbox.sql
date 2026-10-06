-- name: InsertOutboxEvent :exec
INSERT INTO outbox (
    workspace_id,
    event_id,
    event_type,
    event_version,
    occurred_at,
    aggregate_type,
    aggregate_id,
    request_id,
    causation_id,
    payload,
    published_at
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(event_id),
    sqlc.arg(event_type),
    sqlc.arg(event_version),
    sqlc.arg(occurred_at),
    sqlc.arg(aggregate_type),
    sqlc.arg(aggregate_id),
    sqlc.arg(request_id),
    sqlc.arg(causation_id),
    sqlc.arg(payload),
    NULL
);

-- name: ListUnpublishedOutboxEvents :many
SELECT
    workspace_id,
    event_id,
    event_type,
    event_version,
    occurred_at,
    aggregate_type,
    aggregate_id,
    request_id,
    causation_id,
    payload,
    published_at
FROM outbox
WHERE workspace_id = sqlc.arg(workspace_id)
  AND published_at IS NULL
ORDER BY occurred_at, event_id
LIMIT sqlc.arg(batch_size);

-- name: MarkOutboxEventPublished :execrows
UPDATE outbox
SET published_at = sqlc.arg(published_at)
WHERE workspace_id = sqlc.arg(workspace_id)
  AND event_id = sqlc.arg(event_id)
  AND published_at IS NULL;
