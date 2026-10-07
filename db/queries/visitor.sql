-- name: CreateVisitor :exec
INSERT INTO visitors (workspace_id, visitor_id, refresh_token_hash, first_seen_at, last_seen_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(visitor_id), sqlc.arg(refresh_token_hash), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at));

-- name: CreateVisitorSession :exec
INSERT INTO visitor_sessions (token_hash, workspace_id, visitor_id, created_at, last_seen_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(workspace_id), sqlc.arg(visitor_id), sqlc.arg(created_at), sqlc.arg(last_seen_at), sqlc.arg(expires_at));

-- name: GetVisitorSession :one
SELECT s.token_hash, s.workspace_id, s.visitor_id, s.created_at, s.last_seen_at, s.expires_at, v.first_seen_at
FROM visitor_sessions s JOIN visitors v USING (workspace_id, visitor_id)
WHERE s.token_hash = sqlc.arg(token_hash);

-- name: TouchVisitorSession :exec
UPDATE visitor_sessions SET last_seen_at = sqlc.arg(last_seen_at), expires_at = sqlc.arg(expires_at)
WHERE token_hash = sqlc.arg(token_hash);

-- name: TouchVisitor :exec
UPDATE visitors SET last_seen_at = sqlc.arg(last_seen_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND visitor_id = sqlc.arg(visitor_id);

-- name: ListVisitorProfiles :many
SELECT v.visitor_id, v.first_seen_at, v.last_seen_at AS last_activity_at
FROM visitors v
WHERE v.workspace_id = sqlc.arg(workspace_id)
ORDER BY last_activity_at DESC, v.visitor_id DESC;

-- name: DeleteExpiredVisitorSessions :exec
DELETE FROM visitor_sessions WHERE expires_at <= sqlc.arg(expired_at);
