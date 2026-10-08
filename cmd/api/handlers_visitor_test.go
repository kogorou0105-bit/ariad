package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	platformmodel "ariad/internal/platform/model"
)

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
