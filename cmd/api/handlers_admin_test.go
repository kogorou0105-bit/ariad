package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"ariad/internal/conversation"
	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	platformfetch "ariad/internal/platform/fetch"
	platformmodel "ariad/internal/platform/model"
)

func TestManagementEndpointsRequireAdministratorSession(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	path := "/api/v1/conversations?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	validSession := adminSessionForTest(t, router)
	for _, test := range []struct {
		name   string
		header string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong", status: http.StatusUnauthorized},
		{name: "valid", header: "Bearer " + validSession, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
			if test.status == http.StatusUnauthorized && !strings.Contains(response.Body.String(), `"request_id":`) {
				t.Fatalf("401 missing request_id: %s", response.Body.String())
			}
		})
	}

}

func TestAdministratorLoginCreateAndLogout(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	failed := postJSONForTest(t, router, "/api/v1/admin/login", map[string]string{"username": "admin", "password": "wrong"})
	if failed.Code != http.StatusUnauthorized {
		t.Fatalf("failed login status = %d", failed.Code)
	}
	adminSession := adminSessionForTest(t, router)
	created := requestWithSessionForTest(t, router, http.MethodPost, "/api/v1/admin/accounts", adminSession, map[string]string{"username": "operator", "password": "operator-password"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", created.Code, created.Body.String())
	}
	missing := requestWithSessionForTest(t, router, http.MethodDelete, "/api/v1/admin/accounts/missing", adminSession, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d body = %s", missing.Code, missing.Body.String())
	}
	login := postJSONForTest(t, router, "/api/v1/admin/login", map[string]string{"username": "operator", "password": "operator-password"})
	if login.Code != http.StatusOK {
		t.Fatalf("operator login status = %d body = %s", login.Code, login.Body.String())
	}
	var session loginResponse
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/conversations?workspace_id=" + developmentWorkspaceID
	if response := requestWithSessionForTest(t, router, http.MethodGet, path, session.Token, nil); response.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d", response.Code)
	}
	if response := requestWithSessionForTest(t, router, http.MethodPost, "/api/v1/admin/logout", session.Token, nil); response.Code != http.StatusOK {
		t.Fatalf("logout status = %d", response.Code)
	}
	if response := requestWithSessionForTest(t, router, http.MethodGet, path, session.Token, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked status = %d", response.Code)
	}
}

func TestHumanHandoffReviewReplyAndResolveFlow(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	question := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID, "visitor_id": "visitor_review",
		"channel": "widget", "locale": "en", "request_id": "req_review", "idempotency_key": "ik_review", "question": "I need help",
	})
	if question.Code != http.StatusOK {
		t.Fatalf("question status = %d body = %s", question.Code, question.Body.String())
	}
	var answered submitQuestionResponse
	if err := json.Unmarshal(question.Body.Bytes(), &answered); err != nil {
		t.Fatal(err)
	}
	handoffPath := "/api/v1/conversations/" + answered.ConversationID + "/handoff"
	handoff := postJSONForTest(t, router, handoffPath, map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review", "reason": "visitor_requested"})
	if handoff.Code != http.StatusOK {
		t.Fatalf("handoff status = %d body = %s", handoff.Code, handoff.Body.String())
	}
	crossVisitor := postJSONForTest(t, router, handoffPath, map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "someone_else"})
	if crossVisitor.Code != http.StatusNotFound {
		t.Fatalf("cross visitor status = %d", crossVisitor.Code)
	}
	queuePath := "/api/v1/reviews?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	if response := getForTest(t, router, queuePath); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized queue status = %d", response.Code)
	}
	if response := getAdminForTest(t, router, "/api/v1/reviews?workspace_id=other"); response.Code != http.StatusForbidden {
		t.Fatalf("cross workspace queue status = %d", response.Code)
	}
	queue := getAdminForTest(t, router, queuePath)
	if queue.Code != http.StatusOK || !strings.Contains(queue.Body.String(), "visitor_requested") || !strings.Contains(queue.Body.String(), "I need help") {
		t.Fatalf("queue status = %d body = %s", queue.Code, queue.Body.String())
	}
	unauthorizedReply := postJSONForTest(t, router, "/api/v1/reviews/"+answered.ConversationID+"/replies", map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review", "text": "not allowed"})
	if unauthorizedReply.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized reply status = %d", unauthorizedReply.Code)
	}
	crossWorkspaceReply := postAdminJSONForTest(t, router, "/api/v1/reviews/"+answered.ConversationID+"/replies", map[string]string{"workspace_id": "other", "visitor_id": "visitor_review", "text": "not allowed"})
	if crossWorkspaceReply.Code != http.StatusForbidden {
		t.Fatalf("cross workspace reply status = %d", crossWorkspaceReply.Code)
	}
	adminSession := adminSessionForTest(t, router)
	created := requestWithSessionForTest(t, router, http.MethodPost, "/api/v1/admin/accounts", adminSession, map[string]string{"username": "reviewer", "password": "reviewer-password"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create reviewer status = %d body = %s", created.Code, created.Body.String())
	}
	reviewerLogin := postJSONForTest(t, router, "/api/v1/admin/login", map[string]string{"username": "reviewer", "password": "reviewer-password"})
	var reviewerSession loginResponse
	if err := json.Unmarshal(reviewerLogin.Body.Bytes(), &reviewerSession); err != nil {
		t.Fatal(err)
	}
	reply := requestWithSessionForTest(t, router, http.MethodPost, "/api/v1/reviews/"+answered.ConversationID+"/replies", reviewerSession.Token, map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review", "text": "A person is here."})
	if reply.Code != http.StatusCreated || !strings.Contains(reply.Body.String(), `"source":"human"`) || !strings.Contains(reply.Body.String(), `"author_id":"`+reviewerSession.Administrator.AdministratorID+`"`) {
		t.Fatalf("reply status = %d body = %s", reply.Code, reply.Body.String())
	}
	emptyReply := postAdminJSONForTest(t, router, "/api/v1/reviews/"+answered.ConversationID+"/replies", map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review", "text": "   "})
	if emptyReply.Code != http.StatusBadRequest {
		t.Fatalf("empty reply status = %d", emptyReply.Code)
	}
	history := getForTest(t, router, "/api/v1/conversations/"+answered.ConversationID+"?workspace_id="+developmentWorkspaceID+"&visitor_id=visitor_review")
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "A person is here.") || !strings.Contains(history.Body.String(), `"status":"pending"`) {
		t.Fatalf("history status = %d body = %s", history.Code, history.Body.String())
	}
	resolved := postAdminJSONForTest(t, router, "/api/v1/reviews/"+answered.ConversationID+"/resolve", map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review"})
	if resolved.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body = %s", resolved.Code, resolved.Body.String())
	}
	resolvedReply := postAdminJSONForTest(t, router, "/api/v1/reviews/"+answered.ConversationID+"/replies", map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review", "text": "too late"})
	if resolvedReply.Code != http.StatusConflict {
		t.Fatalf("resolved reply status = %d", resolvedReply.Code)
	}
	queue = getAdminForTest(t, router, queuePath)
	if !strings.Contains(queue.Body.String(), `"reviews":[]`) {
		t.Fatalf("resolved queue body = %s", queue.Body.String())
	}
	followUp := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID, "conversation_id": answered.ConversationID, "visitor_id": "visitor_review",
		"channel": "widget", "locale": "en", "request_id": "req_review_followup", "idempotency_key": "ik_review_followup", "question": "One more question",
	})
	if followUp.Code != http.StatusOK {
		t.Fatalf("follow-up status = %d body = %s", followUp.Code, followUp.Body.String())
	}
	history = getForTest(t, router, "/api/v1/conversations/"+answered.ConversationID+"?workspace_id="+developmentWorkspaceID+"&visitor_id=visitor_review")
	if !strings.Contains(history.Body.String(), `"status":"ongoing"`) {
		t.Fatalf("follow-up did not reopen resolved conversation: %s", history.Body.String())
	}
}

func TestModelConfigManagementFlowValidationAndIsolation(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	path := "/api/v1/model-config?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	unauthorized := getForTest(t, router, path)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	crossWorkspace := adminRequestForTest(t, router, http.MethodGet, "/api/v1/model-config?workspace_id=ws_other", nil)
	if crossWorkspace.Code != http.StatusForbidden {
		t.Fatalf("cross-workspace status = %d", crossWorkspace.Code)
	}
	backfillPath := "/api/v1/knowledge/embeddings/backfill?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	if response := getForTest(t, router, backfillPath); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized backfill status = %d", response.Code)
	}
	if response := adminRequestForTest(t, router, http.MethodGet, "/api/v1/knowledge/embeddings/backfill?workspace_id=ws_other", nil); response.Code != http.StatusForbidden {
		t.Fatalf("cross-workspace backfill status = %d", response.Code)
	}
	if response := adminRequestForTest(t, router, http.MethodGet, backfillPath, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"idle"`) {
		t.Fatalf("initial backfill status = %d body = %s", response.Code, response.Body.String())
	}

	for _, test := range []struct {
		name    string
		payload map[string]string
		message string
	}{
		{name: "invalid URL", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "://bad", "model": "m", "api_key": "secret"}, message: "base URL"},
		{name: "missing model", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example", "api_key": "secret"}, message: "model name"},
		{name: "missing first key", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example", "model": "m"}, message: "API key"},
		{name: "invalid embedding URL", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example", "model": "m", "api_key": "secret", "embedding_base_url": "://bad", "embedding_model": "embed", "embedding_api_key": "embedding-secret"}, message: "base URL"},
		{name: "missing first embedding key", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example", "model": "m", "api_key": "secret", "embedding_base_url": "https://embedding.example", "embedding_model": "embed"}, message: "API key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := adminRequestForTest(t, router, http.MethodPut, path, test.payload)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.message) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	secret := "super-secret-workspace-key"
	embeddingSecret := "super-secret-embedding-key"
	saved := adminRequestForTest(t, router, http.MethodPut, path, map[string]any{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example/v1/", "model": "custom-model", "api_key": secret, "embedding_base_url": "https://embedding.example/v1/", "embedding_model": "embed-model", "embedding_api_key": embeddingSecret, "embedding_threshold": 0.42})
	if saved.Code != http.StatusOK || strings.Contains(saved.Body.String(), secret) || strings.Contains(saved.Body.String(), embeddingSecret) {
		t.Fatalf("save status = %d, body leaked = %s", saved.Code, saved.Body.String())
	}
	var configured modelConfigResponse
	if err := json.Unmarshal(saved.Body.Bytes(), &configured); err != nil || !configured.SemanticEnabled || configured.EmbeddingModel != "embed-model" || configured.EmbeddingThreshold != 0.42 || configured.EmbeddingAPIKeyMask == "" {
		t.Fatalf("configured = %#v err = %v", configured, err)
	}
	updated := adminRequestForTest(t, router, http.MethodPut, path, map[string]any{"workspace_id": developmentWorkspaceID, "base_url": "https://new.example", "model": "new-model", "api_key": "", "embedding_base_url": "https://embedding.example/v1", "embedding_model": "embed-model-v2", "embedding_api_key": "", "embedding_threshold": 0.5})
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), secret) || strings.Contains(updated.Body.String(), embeddingSecret) {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body.String())
	}
	reset := adminRequestForTest(t, router, http.MethodDelete, path, nil)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", reset.Code, reset.Body.String())
	}
	var status modelConfigResponse
	if err := json.Unmarshal(reset.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode reset: %v", err)
	}
	if status.Source != "local" {
		t.Fatalf("reset status = %#v", status)
	}
}

func TestSavedModelConfigImmediatelyDrivesVisitorAnswersAndResetRestoresFallback(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var authorization string
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		authorization = request.Header.Get("Authorization")
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode model request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		content := payload.Messages[len(payload.Messages)-1].Content
		start := strings.Index(content, "[ev_")
		if start < 0 {
			t.Errorf("evidence ID missing from prompt")
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		end := strings.Index(content[start:], "]")
		if end < 0 {
			t.Errorf("evidence ID missing from prompt")
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		evidenceID := content[start+1 : start+end]
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"model":"byok-model","choices":[{"message":{"content":"BYOK answer.\nCITATIONS: %s"}}]}`, evidenceID)
	}))
	defer modelServer.Close()

	configs := modelconfig.NewMemoryRepository()
	router := newRouterWithRepositoriesAndModelConfig(
		testLogger(), platformmodel.NewStub(), knowledge.NewMemoryRepository(), conversation.NewMemoryRepository(),
		platformfetch.NewHTTPFetcher(), 0, testAdminPassword, configs, modelconfig.Config{Model: "grounded-v1"},
	)
	configPath := "/api/v1/model-config?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	saved := adminRequestForTest(t, router, http.MethodPut, configPath, map[string]string{
		"workspace_id": developmentWorkspaceID, "base_url": modelServer.URL, "model": "byok-model", "api_key": "byok-secret",
	})
	if saved.Code != http.StatusOK {
		t.Fatalf("save config status = %d, body = %s", saved.Code, saved.Body.String())
	}
	knowledgeResponse := postAdminJSONForTest(t, router, "/api/v1/knowledge/text", map[string]string{
		"workspace_id": developmentWorkspaceID, "request_id": "req_byok_knowledge", "idempotency_key": "ik_byok_knowledge", "title": "Policy", "text": "BYOK evidence.",
	})
	if knowledgeResponse.Code != http.StatusCreated {
		t.Fatalf("knowledge status = %d", knowledgeResponse.Code)
	}
	question := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID, "visitor_id": "visitor_byok", "channel": "widget", "locale": "en", "request_id": "req_byok", "idempotency_key": "ik_byok", "question": "Use BYOK",
	})
	if question.Code != http.StatusOK || authorization != "Bearer byok-secret" || calls.Load() != 1 {
		t.Fatalf("question status = %d, authorization = %q, calls = %d, body = %s", question.Code, authorization, calls.Load(), question.Body.String())
	}
	reset := adminRequestForTest(t, router, http.MethodDelete, configPath, nil)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status = %d", reset.Code)
	}
	fallbackQuestion := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID, "visitor_id": "visitor_fallback", "channel": "widget", "locale": "en", "request_id": "req_fallback", "idempotency_key": "ik_fallback", "question": "Use fallback",
	})
	if fallbackQuestion.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("fallback status = %d, calls = %d", fallbackQuestion.Code, calls.Load())
	}
}
