package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ariad/internal/conversation"
	"ariad/internal/ingestion"
	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	platformfetch "ariad/internal/platform/fetch"
	platformmodel "ariad/internal/platform/model"
)

type staticPageFetcher struct {
	page  ingestion.FetchedPage
	err   error
	calls int
}

const testAdminPassword = "test-admin-token"

type testVisitorSession struct{ token, visitorID string }

var testVisitorSessions sync.Map

func (f *staticPageFetcher) Fetch(context.Context, string) (ingestion.FetchedPage, error) {
	f.calls++
	return f.page, f.err
}

func TestRouterHealth(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newRouter(testLogger(), platformmodel.NewStub()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

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

func TestVisitorConversationEndpointsRequireVisitorSessionNotAdministratorSession(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	question := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID,
		"visitor_id": "visitor_public", "channel": "widget", "locale": "en",
		"request_id": "req_public", "idempotency_key": "ik_public",
		"question": "Can I access this without an admin token?",
	})
	if question.Code != http.StatusOK {
		t.Fatalf("question status = %d, body = %s", question.Code, question.Body.String())
	}
	var answered submitQuestionResponse
	if err := json.Unmarshal(question.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	history := getForTest(t, router, "/api/v1/conversations/"+url.PathEscape(answered.ConversationID)+
		"?workspace_id="+url.QueryEscape(developmentWorkspaceID)+"&visitor_id=visitor_public")
	if history.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", history.Code, history.Body.String())
	}
}

func TestVisitorSessionRejectsInvalidCredentialAndVisitorIDPayload(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	payload := map[string]string{"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID, "visitor_id": "spoofed", "channel": "widget", "locale": "en", "request_id": "req_identity", "idempotency_key": "ik_identity", "question": "Who owns this?"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	invalidRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/questions", bytes.NewReader(body))
	invalidRequest.Header.Set("Authorization", "Bearer invalid")
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnauthorized || !strings.Contains(invalid.Body.String(), `"code":"visitor_session_invalid"`) {
		t.Fatalf("invalid response = %d %s", invalid.Code, invalid.Body.String())
	}

	session := visitorSessionForTest(t, router, "real")
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/questions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+session.token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("spoofed identity response = %d %s", response.Code, response.Body.String())
	}
	delete(payload, "visitor_id")
	body, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/questions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+session.token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("question response = %d %s", response.Code, response.Body.String())
	}
	listed := getAdminForTest(t, router, "/api/v1/conversations?workspace_id="+url.QueryEscape(developmentWorkspaceID))
	if !strings.Contains(listed.Body.String(), `"visitor_id":"`+session.visitorID+`"`) || strings.Contains(listed.Body.String(), `"visitor_id":"spoofed"`) {
		t.Fatalf("ownership response = %s", listed.Body.String())
	}
}

func TestVisitorRefreshKeepsIdentity(t *testing.T) {
	t.Parallel()
	router := newRouter(testLogger(), platformmodel.NewStub())
	body, _ := json.Marshal(map[string]string{"workspace_id": developmentWorkspaceID})
	createdRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/visitor/sessions", bytes.NewReader(body))
	created := httptest.NewRecorder()
	router.ServeHTTP(created, createdRequest)
	var first visitorSessionResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &first) != nil || first.RefreshToken == "" {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	refreshBody, _ := json.Marshal(map[string]string{"workspace_id": developmentWorkspaceID, "refresh_token": first.RefreshToken})
	refreshRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/visitor/sessions/refresh", bytes.NewReader(refreshBody))
	refreshedResponse := httptest.NewRecorder()
	router.ServeHTTP(refreshedResponse, refreshRequest)
	var refreshed visitorSessionResponse
	if refreshedResponse.Code != http.StatusCreated || json.Unmarshal(refreshedResponse.Body.Bytes(), &refreshed) != nil || refreshed.VisitorID != first.VisitorID || refreshed.Token == first.Token {
		t.Fatalf("refresh = %d %s", refreshedResponse.Code, refreshedResponse.Body.String())
	}
	if refreshed.RefreshToken == "" || refreshed.RefreshToken == first.RefreshToken {
		t.Fatalf("refresh credential was not rotated: %s", refreshedResponse.Body.String())
	}
	reusedRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/visitor/sessions/refresh", bytes.NewReader(refreshBody))
	reused := httptest.NewRecorder()
	router.ServeHTTP(reused, reusedRequest)
	if reused.Code != http.StatusUnauthorized || !strings.Contains(reused.Body.String(), `"code":"visitor_refresh_invalid"`) {
		t.Fatalf("reused refresh = %d %s", reused.Code, reused.Body.String())
	}
	invalidBody, _ := json.Marshal(map[string]string{"workspace_id": developmentWorkspaceID, "refresh_token": "invalid"})
	invalidRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/visitor/sessions/refresh", bytes.NewReader(invalidBody))
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid refresh = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestKnowledgeQuestionAnswerFlow(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)

	knowledgeResponse := postAdminJSONForTest(t, router, "/api/v1/knowledge/text", map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"request_id":      "req_knowledge",
		"idempotency_key": "ik_knowledge",
		"title":           "Refund policy",
		"text":            "Refunds are available within 30 days of purchase.",
	})
	if knowledgeResponse.Code != http.StatusCreated {
		t.Fatalf("knowledge status = %d, body = %s", knowledgeResponse.Code, knowledgeResponse.Body.String())
	}

	questionResponse := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"agent_id":        developmentAgentID,
		"conversation_id": "",
		"visitor_id":      "visitor_one",
		"channel":         "widget",
		"locale":          "en",
		"request_id":      "req_question",
		"idempotency_key": "ik_question",
		"question":        "What is the refund window?",
	})
	if questionResponse.Code != http.StatusOK {
		t.Fatalf("question status = %d, body = %s", questionResponse.Code, questionResponse.Body.String())
	}
	var answered submitQuestionResponse
	if err := json.Unmarshal(questionResponse.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	if answered.TerminalDisposition != "answered" {
		t.Fatalf("disposition = %q", answered.TerminalDisposition)
	}
	if answered.MessageID == "" || answered.AnswerID == "" || answered.ConversationID == "" {
		t.Fatalf("stable identifiers missing: %#v", answered)
	}
	if len(answered.Citations) != 1 ||
		answered.Citations[0].Quote != "Refunds are available within 30 days of purchase." {
		t.Fatalf("citations = %#v", answered.Citations)
	}

	secondQuestionResponse := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"agent_id":        developmentAgentID,
		"conversation_id": answered.ConversationID,
		"visitor_id":      "visitor_one",
		"channel":         "widget",
		"locale":          "en",
		"request_id":      "req_question_two",
		"idempotency_key": "ik_question_two",
		"question":        "Do I need a receipt?",
	})
	if secondQuestionResponse.Code != http.StatusOK {
		t.Fatalf(
			"second question status = %d, body = %s",
			secondQuestionResponse.Code,
			secondQuestionResponse.Body.String(),
		)
	}

	historyResponse := getForTest(
		t,
		router,
		"/api/v1/conversations/"+url.PathEscape(answered.ConversationID)+
			"?workspace_id="+url.QueryEscape(developmentWorkspaceID)+
			"&visitor_id=visitor_one",
	)
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", historyResponse.Code, historyResponse.Body.String())
	}
	var history listConversationTurnsResponse
	if err := json.Unmarshal(historyResponse.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(history.Turns) != 2 ||
		history.Turns[0].Message != "What is the refund window?" ||
		history.Turns[1].Message != "Do I need a receipt?" {
		t.Fatalf("history turns = %#v", history.Turns)
	}
	otherVisitorResponse := getForTest(
		t,
		router,
		"/api/v1/conversations/"+url.PathEscape(answered.ConversationID)+
			"?workspace_id="+url.QueryEscape(developmentWorkspaceID)+
			"&visitor_id=visitor_other",
	)
	if otherVisitorResponse.Code != http.StatusOK {
		t.Fatalf("other visitor status = %d", otherVisitorResponse.Code)
	}
	if err := json.Unmarshal(otherVisitorResponse.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode other visitor history: %v", err)
	}
	if len(history.Turns) != 0 {
		t.Fatalf("other visitor turns = %#v, want empty", history.Turns)
	}
}

func TestURLIngestionQuestionAnswerFlowAndReplay(t *testing.T) {
	t.Parallel()
	fetcher := &staticPageFetcher{page: ingestion.FetchedPage{HTML: []byte(
		`<html><head><title>Shipping guide</title></head><body>` +
			`<script>ignore me</script><p>Express shipping arrives in two days.</p></body></html>`,
	)}}
	router := newRouterWithRepositoriesAndBootstrapPassword(
		testLogger(),
		platformmodel.NewStub(),
		knowledge.NewMemoryRepository(),
		conversation.NewMemoryRepository(),
		fetcher,
		0,
		testAdminPassword,
	)
	payload := map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"url":             "https://example.com/shipping",
		"title":           "",
		"request_id":      "req_url",
		"idempotency_key": "ik_url",
	}
	first := postAdminJSONForTest(t, router, "/api/v1/ingestion/url", payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("first ingestion status = %d, body = %s", first.Code, first.Body.String())
	}
	second := postAdminJSONForTest(t, router, "/api/v1/ingestion/url", payload)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay ingestion status = %d, body = %s", second.Code, second.Body.String())
	}
	var firstResult, secondResult submitKnowledgeResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatalf("decode first ingestion: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatalf("decode replay ingestion: %v", err)
	}
	if firstResult.SourceID == "" || firstResult.SourceID != secondResult.SourceID || fetcher.calls != 1 {
		t.Fatalf("ingestion results = %#v, %#v; fetch calls = %d", firstResult, secondResult, fetcher.calls)
	}
	payload["url"] = "https://example.com/different"
	conflict := postAdminJSONForTest(t, router, "/api/v1/ingestion/url", payload)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d; body = %s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}
	var conflictEnvelope errorEnvelope
	if err := json.Unmarshal(conflict.Body.Bytes(), &conflictEnvelope); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflictEnvelope.Error.Code != "idempotency_conflict" || fetcher.calls != 1 {
		t.Fatalf("conflict = %#v; fetch calls = %d", conflictEnvelope.Error, fetcher.calls)
	}

	questionResponse := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"agent_id":        developmentAgentID,
		"conversation_id": "",
		"visitor_id":      "visitor_url",
		"channel":         "widget",
		"locale":          "en",
		"request_id":      "req_url_question",
		"idempotency_key": "ik_url_question",
		"question":        "How long does express shipping take?",
	})
	if questionResponse.Code != http.StatusOK {
		t.Fatalf("question status = %d, body = %s", questionResponse.Code, questionResponse.Body.String())
	}
	var answered submitQuestionResponse
	if err := json.Unmarshal(questionResponse.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	if answered.TerminalDisposition != "answered" || len(answered.Citations) != 1 ||
		answered.Citations[0].Quote != "Express shipping arrives in two days." ||
		answered.Citations[0].SourceTitle != "Shipping guide" {
		t.Fatalf("answer = %#v", answered)
	}
}

func TestURLIngestionReturnsMappedErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		rawURL      string
		page        ingestion.FetchedPage
		fetchError  error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid URL",
			rawURL:      "://missing-scheme",
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
			wantMessage: ingestion.ErrInvalidURL.Error(),
		},
		{
			name:        "unsafe destination",
			fetchError:  ingestion.ErrUnsafeURL,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
			wantMessage: ingestion.ErrUnsafeURL.Error(),
		},
		{
			name:        "empty content",
			page:        ingestion.FetchedPage{HTML: []byte(`<html><body></body></html>`)},
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
			wantMessage: ingestion.ErrNoContent.Error(),
		},
		{
			name:        "remote status",
			fetchError:  fmt.Errorf("%w: HTTP 404", platformfetch.ErrHTTPStatus),
			wantStatus:  http.StatusBadGateway,
			wantCode:    "fetch_failed",
			wantMessage: "HTTP 404",
		},
		{
			name:        "response too large",
			fetchError:  platformfetch.ErrResponseTooLarge,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantCode:    "response_too_large",
			wantMessage: platformfetch.ErrResponseTooLarge.Error(),
		},
		{
			name:        "timeout",
			fetchError:  context.DeadlineExceeded,
			wantStatus:  http.StatusGatewayTimeout,
			wantCode:    "fetch_timeout",
			wantMessage: "URL fetch timed out",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fetcher := &staticPageFetcher{page: test.page, err: test.fetchError}
			router := newRouterWithRepositoriesAndBootstrapPassword(
				testLogger(),
				platformmodel.NewStub(),
				knowledge.NewMemoryRepository(),
				conversation.NewMemoryRepository(),
				fetcher,
				0,
				testAdminPassword,
			)
			rawURL := test.rawURL
			if rawURL == "" {
				rawURL = "https://example.com/page"
			}
			response := postAdminJSONForTest(t, router, "/api/v1/ingestion/url", map[string]string{
				"workspace_id":    developmentWorkspaceID,
				"url":             rawURL,
				"request_id":      "req_error",
				"idempotency_key": "ik_error",
			})
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if envelope.Error.Code != test.wantCode ||
				!strings.Contains(envelope.Error.Message, test.wantMessage) {
				t.Fatalf("error = %#v, want code %q containing %q", envelope.Error, test.wantCode, test.wantMessage)
			}
		})
	}
}

func TestConversationHistoryIsWorkspaceScopedAndEmptyConversationIsEmpty(t *testing.T) {
	t.Parallel()
	router := newRouter(testLogger(), platformmodel.NewStub())
	empty := getForTest(
		t,
		router,
		"/api/v1/conversations/conv_empty?workspace_id="+
			url.QueryEscape(developmentWorkspaceID)+"&visitor_id=visitor_one",
	)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty status = %d, body = %s", empty.Code, empty.Body.String())
	}
	var history listConversationTurnsResponse
	if err := json.Unmarshal(empty.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode empty history: %v", err)
	}
	if history.Turns == nil || len(history.Turns) != 0 {
		t.Fatalf("empty turns = %#v, want non-nil empty list", history.Turns)
	}
	missingVisitor := getForTest(
		t,
		router,
		"/api/v1/conversations/conv_empty?workspace_id="+url.QueryEscape(developmentWorkspaceID),
	)
	if missingVisitor.Code != http.StatusUnauthorized {
		t.Fatalf("missing visitor session status = %d, want %d", missingVisitor.Code, http.StatusUnauthorized)
	}

	forbidden := getForTest(
		t,
		router,
		"/api/v1/conversations/conv_empty?workspace_id=ws_other",
	)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("other workspace status = %d, want %d", forbidden.Code, http.StatusForbidden)
	}
}

func TestConversationListIsWorkspaceScopedEmptyAndNewestFirst(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	empty := getAdminForTest(t, router, "/api/v1/conversations?workspace_id="+url.QueryEscape(developmentWorkspaceID))
	if empty.Code != http.StatusOK {
		t.Fatalf("empty status = %d, body = %s", empty.Code, empty.Body.String())
	}
	var listed listConversationsResponse
	if err := json.Unmarshal(empty.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if listed.Conversations == nil || len(listed.Conversations) != 0 {
		t.Fatalf("empty conversations = %#v", listed.Conversations)
	}
	for _, item := range []struct{ visitor, request, question string }{
		{"visitor_old", "old", "Old conversation"},
		{"visitor_new", "new", "New conversation"},
	} {
		response := postJSONForTest(t, router, "/api/v1/questions", map[string]string{
			"workspace_id": developmentWorkspaceID, "agent_id": developmentAgentID,
			"visitor_id": item.visitor, "channel": "widget", "locale": "en",
			"request_id": "req_" + item.request, "idempotency_key": "ik_" + item.request,
			"question": item.question,
		})
		if response.Code != http.StatusOK {
			t.Fatalf("question status = %d, body = %s", response.Code, response.Body.String())
		}
	}
	response := getAdminForTest(t, router, "/api/v1/conversations?workspace_id="+url.QueryEscape(developmentWorkspaceID))
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Conversations) != 2 || listed.Conversations[0].VisitorID != visitorSessionForTest(t, router, "visitor_new").visitorID || listed.Conversations[1].VisitorID != visitorSessionForTest(t, router, "visitor_old").visitorID {
		t.Fatalf("ordered conversations = %#v", listed.Conversations)
	}
	forbidden := getAdminForTest(t, router, "/api/v1/conversations?workspace_id=ws_other")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("other workspace status = %d, want %d", forbidden.Code, http.StatusForbidden)
	}
}

func TestQuestionRefusesWithoutEvidence(t *testing.T) {
	t.Parallel()
	response := postJSONForTest(
		t,
		newRouter(testLogger(), platformmodel.NewStub()),
		"/api/v1/questions",
		map[string]string{
			"workspace_id":    developmentWorkspaceID,
			"agent_id":        developmentAgentID,
			"conversation_id": "",
			"visitor_id":      "visitor_one",
			"channel":         "widget",
			"locale":          "en",
			"request_id":      "req_question",
			"idempotency_key": "ik_question",
			"question":        "What is the refund window?",
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var refused submitQuestionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &refused); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refused.TerminalDisposition != "refused" {
		t.Fatalf("disposition = %q", refused.TerminalDisposition)
	}
	if len(refused.Citations) != 0 {
		t.Fatalf("refusal citations = %#v", refused.Citations)
	}
}

func TestKnowledgeIdempotencyConflict(t *testing.T) {
	t.Parallel()
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	payload := map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"request_id":      "req_one",
		"idempotency_key": "ik_one",
		"title":           "Policy",
		"text":            "Original text",
	}
	first := postAdminJSONForTest(t, router, "/api/v1/knowledge/text", payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	payload["text"] = "Different text"
	conflict := postAdminJSONForTest(t, router, "/api/v1/knowledge/text", payload)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
}

func TestKnowledgeFileUploadListReprocessAndDelete(t *testing.T) {
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	response := uploadFileForTest(t, router, "policy.txt", []byte("Uploaded refund policy."))
	if response.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", response.Code, response.Body.String())
	}
	var uploaded struct {
		Sources []knowledgeSourceResponse `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil || len(uploaded.Sources) != 1 {
		t.Fatalf("uploaded = %#v err = %v", uploaded, err)
	}
	source := uploaded.Sources[0]
	if source.Status != "ready" || source.ChunkCount != 1 || source.Type != "file" {
		t.Fatalf("source = %#v", source)
	}
	replayed := uploadFileForTest(t, router, "policy.txt", []byte("Uploaded refund policy."))
	var replayPayload struct {
		Sources []knowledgeSourceResponse `json:"sources"`
	}
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayPayload); err != nil || len(replayPayload.Sources) != 1 || replayPayload.Sources[0].SourceID != source.SourceID {
		t.Fatalf("replayed = %s err = %v", replayed.Body.String(), err)
	}
	renamed := uploadFileForTest(t, router, "renamed-policy.txt", []byte("Uploaded refund policy."))
	var renamedPayload struct {
		Sources []knowledgeSourceResponse `json:"sources"`
	}
	if err := json.Unmarshal(renamed.Body.Bytes(), &renamedPayload); err != nil || len(renamedPayload.Sources) != 1 || renamedPayload.Sources[0].SourceID != source.SourceID {
		t.Fatalf("renamed replay = %s err = %v", renamed.Body.String(), err)
	}
	listed := getAdminForTest(t, router, "/api/v1/knowledge/sources?workspace_id="+developmentWorkspaceID)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), source.SourceID) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	reprocessed := adminRequestForTest(t, router, http.MethodPost, "/api/v1/knowledge/sources/"+source.SourceID+"/reprocess", nil)
	if reprocessed.Code != http.StatusOK {
		t.Fatalf("reprocess = %d %s", reprocessed.Code, reprocessed.Body.String())
	}
	deleted := adminRequestForTest(t, router, http.MethodDelete, "/api/v1/knowledge/sources/"+source.SourceID, nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestFailedKnowledgeFileUploadRetryIsIdempotent(t *testing.T) {
	router := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	first := uploadFileForTest(t, router, "unsupported.csv", []byte("column,value"))
	second := uploadFileForTest(t, router, "unsupported.csv", []byte("column,value"))
	var firstPayload, secondPayload struct {
		Sources []knowledgeSourceResponse `json:"sources"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPayload); err != nil {
		t.Fatal(err)
	}
	if len(firstPayload.Sources) != 1 || len(secondPayload.Sources) != 1 || firstPayload.Sources[0].Status != "failed" || firstPayload.Sources[0].SourceID != secondPayload.Sources[0].SourceID {
		t.Fatalf("first = %s second = %s", first.Body.String(), second.Body.String())
	}
	listed := getAdminForTest(t, router, "/api/v1/knowledge/sources?workspace_id="+developmentWorkspaceID)
	var listPayload struct {
		Sources []knowledgeSourceResponse `json:"sources"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	if len(listPayload.Sources) != 1 {
		t.Fatalf("sources = %#v", listPayload.Sources)
	}
}

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

func postJSONForTest(
	t *testing.T,
	handler http.Handler,
	path string,
	payload map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	requestPayload := make(map[string]string, len(payload))
	for key, value := range payload {
		requestPayload[key] = value
	}
	visitorLabel := requestPayload["visitor_id"]
	var visitorSession testVisitorSession
	if visitorLabel != "" {
		visitorSession = visitorSessionForTest(t, handler, visitorLabel)
		if path == "/api/v1/questions" || strings.HasSuffix(path, "/handoff") {
			delete(requestPayload, "visitor_id")
		} else {
			requestPayload["visitor_id"] = visitorSession.visitorID
		}
	}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		path,
		bytes.NewReader(body),
	)
	if path == "/api/v1/questions" || strings.HasSuffix(path, "/handoff") {
		request.Header.Set("Authorization", "Bearer "+visitorSession.token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func postAdminJSONForTest(
	t *testing.T,
	handler http.Handler,
	path string,
	payload map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	requestPayload := make(map[string]string, len(payload))
	for key, value := range payload {
		requestPayload[key] = value
	}
	if label := requestPayload["visitor_id"]; label != "" {
		requestPayload["visitor_id"] = visitorSessionForTest(t, handler, label).visitorID
	}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+adminSessionForTest(t, handler))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func uploadFileForTest(t *testing.T, handler http.Handler, fileName string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("workspace_id", developmentWorkspaceID); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("files", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/knowledge/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+adminSessionForTest(t, handler))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body = %s", response.Code, response.Body.String())
	}
	return response
}

func getForTest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	visitorLabel := parsed.Query().Get("visitor_id")
	var session testVisitorSession
	if visitorLabel != "" {
		session = visitorSessionForTest(t, handler, visitorLabel)
		query := parsed.Query()
		query.Set("visitor_id", session.visitorID)
		parsed.RawQuery = query.Encode()
		path = parsed.String()
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if strings.HasPrefix(parsed.Path, "/api/v1/conversations/") && session.token != "" {
		request.Header.Set("Authorization", "Bearer "+session.token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func visitorSessionForTest(t *testing.T, handler http.Handler, label string) testVisitorSession {
	t.Helper()
	key := fmt.Sprintf("%p:%s", handler, label)
	if cached, ok := testVisitorSessions.Load(key); ok {
		return cached.(testVisitorSession)
	}
	body, err := json.Marshal(map[string]string{"workspace_id": developmentWorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/visitor/sessions", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("visitor session status = %d body = %s", response.Code, response.Body.String())
	}
	var issued visitorSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	session := testVisitorSession{token: issued.Token, visitorID: issued.VisitorID}
	actual, _ := testVisitorSessions.LoadOrStore(key, session)
	return actual.(testVisitorSession)
}

func getAdminForTest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+adminSessionForTest(t, handler))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminRequestForTest(t *testing.T, handler http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequestWithContext(context.Background(), method, path, body)
	request.Header.Set("Authorization", "Bearer "+adminSessionForTest(t, handler))
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminSessionForTest(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := postJSONForTest(t, handler, "/api/v1/admin/login", map[string]string{"username": "admin", "password": testAdminPassword})
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", response.Code, response.Body.String())
	}
	var login loginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return login.Token
}

func requestWithSessionForTest(t *testing.T, handler http.Handler, method, path, token string, payload map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		requestPayload := make(map[string]string, len(payload))
		for key, value := range payload {
			requestPayload[key] = value
		}
		if label := requestPayload["visitor_id"]; label != "" {
			requestPayload["visitor_id"] = visitorSessionForTest(t, handler, label).visitorID
		}
		var err error
		body, err = json.Marshal(requestPayload)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
