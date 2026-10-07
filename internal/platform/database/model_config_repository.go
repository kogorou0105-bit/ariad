package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	dbgen "ariad/db/generated"
	"ariad/internal/modelconfig"
)

type ModelConfigRepository struct {
	queries *dbgen.Queries
	aead    cipher.AEAD
}

var _ modelconfig.Repository = (*ModelConfigRepository)(nil)

func NewModelConfigRepository(database *sql.DB, encodedKey string) (*ModelConfigRepository, error) {
	if encodedKey == "" {
		return &ModelConfigRepository{queries: dbgen.New(database)}, nil
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("ARIAD_MODEL_CONFIG_ENCRYPTION_KEY must be Base64-encoded 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create model config cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create model config AEAD: %w", err)
	}
	return &ModelConfigRepository{queries: dbgen.New(database), aead: aead}, nil
}

func (r *ModelConfigRepository) Get(ctx context.Context, workspaceID string) (modelconfig.Config, bool, error) {
	if r.aead == nil {
		return modelconfig.Config{}, false, modelconfig.ErrEncryptionDisabled
	}
	row, err := r.queries.GetWorkspaceModelConfig(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return modelconfig.Config{}, false, nil
	}
	if err != nil {
		return modelconfig.Config{}, false, fmt.Errorf("query workspace model config: %w", err)
	}
	apiKey, err := r.decrypt(row.ApiKeyCiphertext, workspaceID)
	if err != nil {
		return modelconfig.Config{}, false, err
	}
	return modelconfig.Config{WorkspaceID: row.WorkspaceID, BaseURL: row.BaseUrl, Model: row.Model, APIKey: apiKey, UpdatedAt: row.UpdatedAt}, true, nil
}

func (r *ModelConfigRepository) Save(ctx context.Context, config modelconfig.Config) error {
	if r.aead == nil {
		return modelconfig.ErrEncryptionDisabled
	}
	ciphertext, err := r.encrypt(config.APIKey, config.WorkspaceID)
	if err != nil {
		return err
	}
	return r.queries.UpsertWorkspaceModelConfig(ctx, dbgen.UpsertWorkspaceModelConfigParams{
		WorkspaceID: config.WorkspaceID, BaseUrl: config.BaseURL, Model: config.Model,
		ApiKeyCiphertext: ciphertext, UpdatedAt: config.UpdatedAt,
	})
}

func (r *ModelConfigRepository) Delete(ctx context.Context, workspaceID string) error {
	if r.aead == nil {
		return modelconfig.ErrEncryptionDisabled
	}
	return r.queries.DeleteWorkspaceModelConfig(ctx, workspaceID)
}

func (r *ModelConfigRepository) encrypt(value, workspaceID string) (string, error) {
	nonce := make([]byte, r.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate model config nonce: %w", err)
	}
	sealed := r.aead.Seal(nil, nonce, []byte(value), []byte(workspaceID))
	payload := append(nonce, sealed...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (r *ModelConfigRepository) decrypt(encoded, workspaceID string) (string, error) {
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(payload) < r.aead.NonceSize() {
		return "", errors.New("decrypt workspace model API key: invalid ciphertext")
	}
	nonce, ciphertext := payload[:r.aead.NonceSize()], payload[r.aead.NonceSize():]
	plaintext, err := r.aead.Open(nil, nonce, ciphertext, []byte(workspaceID))
	if err != nil {
		return "", errors.New("decrypt workspace model API key: authentication failed")
	}
	return string(plaintext), nil
}
