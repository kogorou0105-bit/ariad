-- name: InsertUsageFact :exec
INSERT INTO usage_facts (
    workspace_id,
    deduplication_key,
    agent_id,
    request_id,
    answer_id,
    provider,
    model,
    input_units,
    cached_input_units,
    output_units,
    status,
    occurred_at
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(deduplication_key),
    sqlc.arg(agent_id),
    sqlc.arg(request_id),
    sqlc.arg(answer_id),
    sqlc.arg(provider),
    sqlc.arg(model),
    sqlc.arg(input_units),
    sqlc.arg(cached_input_units),
    sqlc.arg(output_units),
    sqlc.arg(status),
    sqlc.arg(occurred_at)
);
