-- +goose Up

CREATE TABLE workspace_model_configs (
    workspace_id text NOT NULL,
    base_url text NOT NULL,
    model text NOT NULL,
    api_key_ciphertext text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id),
    CONSTRAINT workspace_model_configs_workspace_id_not_empty CHECK (btrim(workspace_id) <> ''),
    CONSTRAINT workspace_model_configs_base_url_not_empty CHECK (btrim(base_url) <> ''),
    CONSTRAINT workspace_model_configs_model_not_empty CHECK (btrim(model) <> ''),
    CONSTRAINT workspace_model_configs_api_key_ciphertext_not_empty CHECK (btrim(api_key_ciphertext) <> '')
);

-- +goose Down

DROP TABLE workspace_model_configs;
