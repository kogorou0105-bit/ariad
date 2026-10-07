-- name: GetWorkspaceModelConfig :one
SELECT workspace_id, base_url, model, api_key_ciphertext, updated_at
FROM workspace_model_configs
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceModelConfig :exec
INSERT INTO workspace_model_configs (workspace_id, base_url, model, api_key_ciphertext, updated_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(base_url), sqlc.arg(model), sqlc.arg(api_key_ciphertext), sqlc.arg(updated_at))
ON CONFLICT (workspace_id) DO UPDATE SET
    base_url = EXCLUDED.base_url,
    model = EXCLUDED.model,
    api_key_ciphertext = EXCLUDED.api_key_ciphertext,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteWorkspaceModelConfig :exec
DELETE FROM workspace_model_configs WHERE workspace_id = sqlc.arg(workspace_id);
