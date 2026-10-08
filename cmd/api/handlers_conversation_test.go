package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	platformmodel "ariad/internal/platform/model"
)

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
