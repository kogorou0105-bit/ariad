-- name: GetConversationMessageByIdempotencyKey :one
SELECT
    message_id,
    workspace_id,
    conversation_id,
    visitor_id,
    channel,
    locale,
    text,
    payload_fingerprint,
    created_at
FROM conversation_messages
WHERE workspace_id = sqlc.arg(workspace_id)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: GetConversationAnswerByMessageID :one
SELECT
    answer_id,
    workspace_id,
    conversation_id,
    message_id,
    agent_id,
    terminal_disposition,
    text,
    created_at
FROM conversation_answers
WHERE workspace_id = sqlc.arg(workspace_id)
  AND message_id = sqlc.arg(message_id);

-- name: ListConversationCitationsByAnswerID :many
SELECT
    citation_id,
    evidence_id,
    source_id,
    chunk_id,
    source_title,
    quote
FROM conversation_citations
WHERE workspace_id = sqlc.arg(workspace_id)
  AND answer_id = sqlc.arg(answer_id)
ORDER BY ordinal;

-- name: ConversationExists :one
SELECT EXISTS (
    SELECT 1
    FROM conversation_messages
    WHERE workspace_id = sqlc.arg(workspace_id)
      AND conversation_id = sqlc.arg(conversation_id)
      AND visitor_id = sqlc.arg(visitor_id)
);

-- name: GetConversationVisitor :one
SELECT visitor_id
FROM conversation_messages
WHERE workspace_id = sqlc.arg(workspace_id)
  AND conversation_id = sqlc.arg(conversation_id)
ORDER BY created_at, message_id
LIMIT 1;

-- name: LockConversationSubmission :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'conversation-submission:' || sqlc.arg(workspace_id) || ':' || sqlc.arg(idempotency_key),
    0
));

-- name: LockConversation :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'conversation:' || sqlc.arg(workspace_id) || ':' || sqlc.arg(conversation_id),
    0
));

-- name: InsertConversationMessage :exec
INSERT INTO conversation_messages (
    workspace_id,
    message_id,
    conversation_id,
    visitor_id,
    channel,
    locale,
    text,
    idempotency_key,
    payload_fingerprint,
    created_at
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(message_id),
    sqlc.arg(conversation_id),
    sqlc.arg(visitor_id),
    sqlc.arg(channel),
    sqlc.arg(locale),
    sqlc.arg(text),
    sqlc.arg(idempotency_key),
    sqlc.arg(payload_fingerprint),
    sqlc.arg(created_at)
);

-- name: InsertConversationAnswer :exec
INSERT INTO conversation_answers (
    workspace_id,
    answer_id,
    conversation_id,
    message_id,
    agent_id,
    terminal_disposition,
    text,
    created_at
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(answer_id),
    sqlc.arg(conversation_id),
    sqlc.arg(message_id),
    sqlc.arg(agent_id),
    sqlc.arg(terminal_disposition),
    sqlc.arg(text),
    sqlc.arg(created_at)
);

-- name: InsertConversationCitation :exec
INSERT INTO conversation_citations (
    workspace_id,
    citation_id,
    answer_id,
    evidence_id,
    source_id,
    chunk_id,
    source_title,
    quote,
    ordinal
) VALUES (
    sqlc.arg(workspace_id),
    sqlc.arg(citation_id),
    sqlc.arg(answer_id),
    sqlc.arg(evidence_id),
    sqlc.arg(source_id),
    sqlc.arg(chunk_id),
    sqlc.arg(source_title),
    sqlc.arg(quote),
    sqlc.arg(ordinal)
);
