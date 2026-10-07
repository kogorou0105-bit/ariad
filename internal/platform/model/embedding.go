package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"ariad/internal/modelconfig"
	"ariad/internal/retrieval"
)

// OpenAIEmbedder calls an OpenAI-compatible /embeddings endpoint. A local
// OpenAI-compatible provider works by setting the same base URL and API key.
type OpenAIEmbedder struct {
	endpoint, apiKey, model string
	client                  *http.Client
}

var _ retrieval.Embedder = (*OpenAIEmbedder)(nil)

func NewEmbedderFromEnvironment() (retrieval.Embedder, error) {
	settings := SettingsFromEnvironment()
	model := EmbeddingModelFromEnvironment()
	if settings.APIKey == "" || model == "" {
		return nil, nil
	}
	return &OpenAIEmbedder{endpoint: strings.TrimRight(settings.BaseURL, "/") + "/embeddings", apiKey: settings.APIKey, model: model, client: &http.Client{Timeout: modelHTTPTimeout}}, nil
}

func EmbeddingModelFromEnvironment() string {
	return strings.TrimSpace(getenv("ARIAD_EMBEDDING_MODEL"))
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, _ string, text string) ([]float64, error) {
	body, err := json.Marshal(map[string]any{"model": e.model, "input": text})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+e.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := e.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding provider returned HTTP status %d", response.StatusCode)
	}
	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Data) != 1 || len(payload.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding provider returned no vector")
	}
	return payload.Data[0].Embedding, nil
}

type WorkspaceEmbedder struct {
	configs  modelconfig.Reader
	fallback retrieval.Embedder
}
type EmbeddingHealth struct {
	Attempted   bool      `json:"attempted"`
	AttemptedAt time.Time `json:"attempted_at"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
}

var embeddingHealth = struct {
	sync.RWMutex
	values map[string]EmbeddingHealth
}{values: make(map[string]EmbeddingHealth)}

func GetEmbeddingHealth(workspaceID string) EmbeddingHealth {
	embeddingHealth.RLock()
	defer embeddingHealth.RUnlock()
	return embeddingHealth.values[workspaceID]
}

func NewWorkspaceEmbedder(configs modelconfig.Reader, fallback retrieval.Embedder) *WorkspaceEmbedder {
	return &WorkspaceEmbedder{configs: configs, fallback: fallback}
}
func (e *WorkspaceEmbedder) Embed(ctx context.Context, workspaceID, text string) ([]float64, error) {
	config, found, err := e.configs.Get(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if !found || config.EmbeddingModel == "" {
		if e.fallback == nil {
			return nil, nil
		}
		return e.fallback.Embed(ctx, workspaceID, text)
	}
	adapter := &OpenAIEmbedder{endpoint: strings.TrimRight(config.EmbeddingBaseURL, "/") + "/embeddings", apiKey: config.EmbeddingAPIKey, model: config.EmbeddingModel, client: &http.Client{Timeout: modelHTTPTimeout}}
	vector, embedErr := adapter.Embed(ctx, workspaceID, text)
	health := EmbeddingHealth{Attempted: true, AttemptedAt: time.Now().UTC(), Success: embedErr == nil}
	if embedErr != nil {
		health.Error = embedErr.Error()
	}
	embeddingHealth.Lock()
	embeddingHealth.values[workspaceID] = health
	embeddingHealth.Unlock()
	return vector, embedErr
}
func (e *WorkspaceEmbedder) SemanticThreshold(ctx context.Context, workspaceID string) (float64, error) {
	config, found, err := e.configs.Get(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	if found && config.EmbeddingThreshold > 0 {
		return config.EmbeddingThreshold, nil
	}
	return 0.35, nil
}

func getenv(name string) string { return strings.TrimSpace(os.Getenv(name)) }
