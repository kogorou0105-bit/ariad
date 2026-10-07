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

-- name: ListConversations :many
SELECT
    m.conversation_id,
    m.visitor_id,
    COUNT(*) AS message_count,
    MIN(m.created_at)::timestamptz AS started_at,
    MAX(m.created_at)::timestamptz AS last_activity_at,
    ((ARRAY_AGG(m.text ORDER BY m.created_at DESC, m.message_id DESC))[1])::text AS last_message_text,
    CASE
      WHEN MAX(m.created_at) < now() - interval '30 days' AND COALESCE(MAX(s.status), 'ongoing') = 'ongoing' THEN 'expired'
      ELSE COALESCE(MAX(s.status), 'ongoing')
    END::text AS status
FROM conversation_messages m
LEFT JOIN conversation_states s USING (workspace_id, conversation_id, visitor_id)
WHERE m.workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.narg(visitor_id)::text IS NULL OR m.visitor_id = sqlc.narg(visitor_id))
GROUP BY m.conversation_id, m.visitor_id
ORDER BY last_activity_at DESC, m.conversation_id DESC;

-- name: ListConversationTurns :many
WITH selected_messages AS (
    SELECT
        message_id,
        workspace_id,
        conversation_id,
        visitor_id,
        channel,
        locale,
        text,
        created_at
    FROM conversation_messages
    WHERE conversation_messages.workspace_id = sqlc.arg(workspace_id)
      AND conversation_messages.conversation_id = sqlc.arg(conversation_id)
      AND conversation_messages.visitor_id = sqlc.arg(visitor_id)
    ORDER BY conversation_messages.created_at DESC, conversation_messages.message_id DESC
    LIMIT NULLIF(sqlc.arg(turn_limit)::integer, 0)
)
SELECT
    messages.message_id,
    messages.workspace_id,
    messages.conversation_id,
    messages.visitor_id,
    messages.channel,
    messages.locale,
    messages.text AS message_text,
    messages.created_at AS message_created_at,
    answers.answer_id,
    answers.agent_id,
    answers.terminal_disposition,
    answers.text AS answer_text,
    answers.created_at AS answer_created_at,
    citations.citation_id,
    citations.evidence_id,
    citations.source_id,
    citations.chunk_id,
    citations.source_title,
    citations.quote,
    citations.ordinal AS citation_ordinal
FROM selected_messages AS messages
JOIN conversation_answers AS answers
  ON answers.workspace_id = messages.workspace_id
 AND answers.message_id = messages.message_id
 AND answers.conversation_id = messages.conversation_id
LEFT JOIN conversation_citations AS citations
  ON citations.workspace_id = answers.workspace_id
 AND citations.answer_id = answers.answer_id
ORDER BY messages.created_at, messages.message_id, citations.ordinal;

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
