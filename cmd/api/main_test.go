package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	platformmodel "ariad/internal/platform/model"
)

func TestRouterHealth(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newRouter(testLogger(), platformmodel.NewStub()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestKnowledgeQuestionAnswerFlow(t *testing.T) {
	t.Parallel()
	router := newRouter(testLogger(), platformmodel.NewStub())

	knowledgeResponse := postJSONForTest(t, router, "/api/v1/knowledge/text", map[string]string{
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
	if missingVisitor.Code != http.StatusBadRequest {
		t.Fatalf("missing visitor status = %d, want %d", missingVisitor.Code, http.StatusBadRequest)
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
	router := newRouter(testLogger(), platformmodel.NewStub())
	payload := map[string]string{
		"workspace_id":    developmentWorkspaceID,
		"request_id":      "req_one",
		"idempotency_key": "ik_one",
		"title":           "Policy",
		"text":            "Original text",
	}
	first := postJSONForTest(t, router, "/api/v1/knowledge/text", payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	payload["text"] = "Different text"
	conflict := postJSONForTest(t, router, "/api/v1/knowledge/text", payload)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", conflict.Code, conflict.Body.String())
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
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		path,
		bytes.NewReader(body),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func getForTest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
