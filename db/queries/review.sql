-- name: EnsureConversationState :exec
INSERT INTO conversation_states (workspace_id, conversation_id, visitor_id, status, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(conversation_id), sqlc.arg(visitor_id), 'ongoing', sqlc.arg(updated_at))
ON CONFLICT (workspace_id, conversation_id) DO NOTHING;

-- name: MarkConversationPending :exec
INSERT INTO conversation_states (
    workspace_id, conversation_id, visitor_id, status, handoff_reason,
    handoff_requested_by, handoff_requested_at, updated_at
) VALUES (
    sqlc.arg(workspace_id), sqlc.arg(conversation_id), sqlc.arg(visitor_id), 'pending',
    sqlc.arg(handoff_reason), sqlc.arg(requested_by), sqlc.arg(requested_at), sqlc.arg(requested_at)
)
ON CONFLICT (workspace_id, conversation_id) DO UPDATE SET
    status = 'pending', handoff_reason = EXCLUDED.handoff_reason,
    handoff_requested_by = EXCLUDED.handoff_requested_by,
    handoff_requested_at = EXCLUDED.handoff_requested_at,
    resolved_by = '', resolved_at = NULL, updated_at = EXCLUDED.updated_at;

-- name: ResolveConversation :execrows
UPDATE conversation_states SET status = 'resolved', resolved_by = sqlc.arg(resolved_by),
    resolved_at = sqlc.arg(resolved_at), updated_at = sqlc.arg(resolved_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND conversation_id = sqlc.arg(conversation_id)
  AND status = 'pending';

-- name: ReopenResolvedConversation :exec
UPDATE conversation_states SET status = 'ongoing', resolved_by = '', resolved_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND conversation_id = sqlc.arg(conversation_id)
  AND visitor_id = sqlc.arg(visitor_id) AND status = 'resolved';

-- name: GetConversationState :one
SELECT workspace_id, conversation_id, visitor_id, status, handoff_reason,
       handoff_requested_by, handoff_requested_at, resolved_by, resolved_at, updated_at
FROM conversation_states
WHERE workspace_id = sqlc.arg(workspace_id) AND conversation_id = sqlc.arg(conversation_id)
  AND visitor_id = sqlc.arg(visitor_id);

-- name: ListPendingReviews :many
SELECT s.conversation_id, s.visitor_id, s.handoff_reason, s.handoff_requested_by,
       s.handoff_requested_at, s.updated_at,
       (SELECT m.text FROM conversation_messages m WHERE m.workspace_id = s.workspace_id
        AND m.conversation_id = s.conversation_id ORDER BY m.created_at DESC, m.message_id DESC LIMIT 1) AS last_message_text
FROM conversation_states s
WHERE s.workspace_id = sqlc.arg(workspace_id) AND s.status = 'pending'
ORDER BY s.updated_at DESC, s.conversation_id DESC;

-- name: InsertHumanReply :execrows
INSERT INTO conversation_human_replies (
    workspace_id, reply_id, conversation_id, visitor_id, author_id, text, created_at
) SELECT
    sqlc.arg(workspace_id), sqlc.arg(reply_id), sqlc.arg(conversation_id),
    sqlc.arg(visitor_id), sqlc.arg(author_id), sqlc.arg(text), sqlc.arg(created_at)
WHERE EXISTS (
    SELECT 1 FROM conversation_states
    WHERE workspace_id = sqlc.arg(workspace_id) AND conversation_id = sqlc.arg(conversation_id)
      AND visitor_id = sqlc.arg(visitor_id) AND status = 'pending'
);

-- name: TouchConversationState :exec
UPDATE conversation_states SET updated_at = sqlc.arg(updated_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND conversation_id = sqlc.arg(conversation_id);

-- name: ListHumanReplies :many
SELECT reply_id, conversation_id, visitor_id, author_id, text, created_at
FROM conversation_human_replies
WHERE workspace_id = sqlc.arg(workspace_id) AND conversation_id = sqlc.arg(conversation_id)
  AND visitor_id = sqlc.arg(visitor_id)
ORDER BY created_at, reply_id;
