package model_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	platformmodel "ariad/internal/platform/model"
	"ariad/internal/retrieval"
)

func TestOpenAICompatibleEmbeddingEndToEnd(t *testing.T) {
	t.Parallel()
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/embeddings" || request.Header.Get("Authorization") != "Bearer embedding-secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		var input struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Model != "test-embedding" {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		lower := strings.ToLower(input.Input)
		vector := []float64{0, 1}
		if strings.Contains(lower, "退款") || strings.Contains(lower, "refund") || strings.Contains(lower, "money back") {
			vector = []float64{1, 0}
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"data": []any{map[string]any{"embedding": vector}}})
	}))
	defer provider.Close()

	configs := modelconfig.NewMemoryRepository()
	if err := configs.Save(context.Background(), modelconfig.Config{
		WorkspaceID: "ws", BaseURL: provider.URL, Model: "chat", APIKey: "chat-secret",
		EmbeddingBaseURL: provider.URL, EmbeddingModel: "test-embedding", EmbeddingAPIKey: "embedding-secret",
		EmbeddingThreshold: 0.35, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	embedder := platformmodel.NewWorkspaceEmbedder(configs, nil)
	repository := knowledge.NewMemoryRepository()
	knowledgeService := knowledge.NewService(repository, knowledge.WithEmbedder(embedder))
	if _, err := knowledgeService.SubmitText(context.Background(), knowledge.SubmitTextCommand{
		WorkspaceID: "ws", IdempotencyKey: "refund", Title: "退款政策", Text: "退款申请必须在购买后 30 天内提交",
	}); err != nil {
		t.Fatal(err)
	}
	retrievalService := retrieval.NewService(repository, retrieval.WithEmbedder(embedder))
	for _, question := range []string{"多久之内可以退款", "How do I get my money back?", "What is the refund window?"} {
		evidence, err := retrievalService.Retrieve(context.Background(), retrieval.Query{WorkspaceID: "ws", Question: question})
		if err != nil || len(evidence) != 1 || evidence[0].SourceTitle != "退款政策" {
			t.Fatalf("retrieve %q = %#v, %v", question, evidence, err)
		}
	}
	health := platformmodel.GetEmbeddingHealth("ws")
	if !health.Attempted || !health.Success || health.Error != "" {
		t.Fatalf("embedding health = %#v", health)
	}
}

func TestEmbeddingProviderFailureIsReportedWithoutLeakingKey(t *testing.T) {
	t.Parallel()
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer provider.Close()
	configs := modelconfig.NewMemoryRepository()
	if err := configs.Save(context.Background(), modelconfig.Config{
		WorkspaceID: "ws_embedding_failure", BaseURL: provider.URL, Model: "chat", APIKey: "chat-secret",
		EmbeddingBaseURL: provider.URL, EmbeddingModel: "test-embedding", EmbeddingAPIKey: "never-leak-this-key",
		EmbeddingThreshold: 0.35, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	embedder := platformmodel.NewWorkspaceEmbedder(configs, nil)
	_, err := embedder.Embed(context.Background(), "ws_embedding_failure", "question")
	if err == nil || strings.Contains(err.Error(), "never-leak-this-key") {
		t.Fatalf("error = %v", err)
	}
	health := platformmodel.GetEmbeddingHealth("ws_embedding_failure")
	if !health.Attempted || health.Success || health.Error == "" || strings.Contains(health.Error, "never-leak-this-key") {
		t.Fatalf("embedding health = %#v", health)
	}
}
