package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"ariad/internal/conversation"
	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	platformmodel "ariad/internal/platform/model"
)

func TestPlaygroundAPIValidationAndConfigurationErrors(t *testing.T) {
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	invalidPayloads := []map[string]any{
		{"workspace_id": developmentWorkspaceID, "question": "test", "top_k": 0, "threshold": .3},
		{"workspace_id": developmentWorkspaceID, "question": "test", "top_k": 21, "threshold": .3},
		{"workspace_id": developmentWorkspaceID, "question": "test", "top_k": 5, "threshold": 1.1},
		{"workspace_id": developmentWorkspaceID, "question": " ", "top_k": 5, "threshold": .3},
	}
	for _, payload := range invalidPayloads {
		invalid := adminRequestForTest(t, router, http.MethodPost, "/api/v1/playground/ask", payload)
		if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("invalid = %d %s", invalid.Code, invalid.Body.String())
		}
	}
	missingModel := adminRequestForTest(t, router, http.MethodPost, "/api/v1/playground/ask", map[string]any{"workspace_id": developmentWorkspaceID, "question": "test", "top_k": 5, "threshold": .3})
	if missingModel.Code != http.StatusPreconditionFailed || !strings.Contains(missingModel.Body.String(), `"code":"model_not_configured"`) {
		t.Fatalf("missing model = %d %s", missingModel.Code, missingModel.Body.String())
	}
}

func TestPlaygroundAPIReturnsNoKnowledgeAndDebugEvidence(t *testing.T) {
	repository := knowledge.NewMemoryRepository()
	configured := modelconfig.Config{WorkspaceID: developmentWorkspaceID, BaseURL: "https://model.example", Model: "fake", APIKey: "fake-key"}
	router := newRouterWithRepositoriesAndModelConfig(testLogger(), platformmodel.NewStub(), repository, conversation.NewMemoryRepository(), &staticPageFetcher{}, 0, testAdminPassword, modelconfig.NewMemoryRepository(), configured)
	payload := map[string]any{"workspace_id": developmentWorkspaceID, "question": "What is the refund policy?", "top_k": 3, "threshold": 0.0, "locale": "en"}
	empty := adminRequestForTest(t, router, http.MethodPost, "/api/v1/playground/ask", payload)
	if empty.Code != http.StatusUnprocessableEntity || !strings.Contains(empty.Body.String(), `"code":"no_knowledge"`) {
		t.Fatalf("empty = %d %s", empty.Code, empty.Body.String())
	}
	created := postAdminJSONForTest(t, router, "/api/v1/knowledge/text", map[string]string{"workspace_id": developmentWorkspaceID, "request_id": "req-playground", "idempotency_key": "ik-playground", "title": "Refund policy", "text": "Refunds are available within 30 days."})
	if created.Code != http.StatusCreated {
		t.Fatalf("knowledge = %d %s", created.Code, created.Body.String())
	}
	answered := adminRequestForTest(t, router, http.MethodPost, "/api/v1/playground/ask", payload)
	if answered.Code != http.StatusOK {
		t.Fatalf("answered = %d %s", answered.Code, answered.Body.String())
	}
	var result playgroundAskResponse
	if err := json.Unmarshal(answered.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "answered" || result.HitCount != 1 || len(result.Hits) != 1 || result.Hits[0].SourceTitle != "Refund policy" || result.Hits[0].Score <= 0 {
		t.Fatalf("result = %#v", result)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
