-- name: CountAdministrators :one
SELECT count(*) FROM administrators;

-- name: CreateAdministrator :exec
INSERT INTO administrators (administrator_id, username, password_hash, created_at)
VALUES (sqlc.arg(administrator_id), sqlc.arg(username), sqlc.arg(password_hash), sqlc.arg(created_at));

-- name: FindAdministratorByUsername :one
SELECT administrator_id, username, password_hash, created_at FROM administrators
WHERE username = sqlc.arg(username);

-- name: FindAdministratorByID :one
SELECT administrator_id, username, created_at FROM administrators
WHERE administrator_id = sqlc.arg(administrator_id);

-- name: ListAdministrators :many
SELECT administrator_id, username, created_at FROM administrators ORDER BY username;

-- name: UpdateAdministratorPassword :exec
UPDATE administrators SET password_hash = sqlc.arg(password_hash)
WHERE administrator_id = sqlc.arg(administrator_id);

-- name: CreateAdministratorSession :exec
INSERT INTO administrator_sessions (token_hash, administrator_id, created_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(administrator_id), sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: FindValidAdministratorSession :one
SELECT token_hash, administrator_id, created_at, expires_at FROM administrator_sessions
WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now);

-- name: DeleteAdministratorSession :exec
DELETE FROM administrator_sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteAdministratorSessions :exec
DELETE FROM administrator_sessions WHERE administrator_id = sqlc.arg(administrator_id);

-- name: DeleteExpiredAdministratorSessions :exec
DELETE FROM administrator_sessions WHERE expires_at <= sqlc.arg(now);
