package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

const testAdminToken = "test-admin-token"

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

func TestManagementEndpointsRequireAdminToken(t *testing.T) {
	t.Parallel()
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)
	path := "/api/v1/conversations?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	for _, test := range []struct {
		name   string
		header string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong", status: http.StatusUnauthorized},
		{name: "valid", header: "Bearer " + testAdminToken, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
		})
	}

	unconfigured := getForTest(t, newRouter(testLogger(), platformmodel.NewStub()), path)
	if unconfigured.Code != http.StatusUnauthorized {
		t.Fatalf("unconfigured status = %d, want %d", unconfigured.Code, http.StatusUnauthorized)
	}
}

func TestHumanHandoffReviewReplyAndResolveFlow(t *testing.T) {
	t.Parallel()
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)
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
	reply := postAdminJSONForTest(t, router, "/api/v1/reviews/"+answered.ConversationID+"/replies", map[string]string{"workspace_id": developmentWorkspaceID, "visitor_id": "visitor_review", "text": "A person is here."})
	if reply.Code != http.StatusCreated || !strings.Contains(reply.Body.String(), `"source":"human"`) {
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
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)
	path := "/api/v1/model-config?workspace_id=" + url.QueryEscape(developmentWorkspaceID)
	unauthorized := getForTest(t, router, path)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	crossWorkspace := adminRequestForTest(t, router, http.MethodGet, "/api/v1/model-config?workspace_id=ws_other", nil)
	if crossWorkspace.Code != http.StatusForbidden {
		t.Fatalf("cross-workspace status = %d", crossWorkspace.Code)
	}

	for _, test := range []struct {
		name    string
		payload map[string]string
		message string
	}{
		{name: "invalid URL", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "://bad", "model": "m", "api_key": "secret"}, message: "base URL"},
		{name: "missing model", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example", "api_key": "secret"}, message: "model name"},
		{name: "missing first key", payload: map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example", "model": "m"}, message: "API key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := adminRequestForTest(t, router, http.MethodPut, path, test.payload)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.message) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	secret := "super-secret-workspace-key"
	saved := adminRequestForTest(t, router, http.MethodPut, path, map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://model.example/v1/", "model": "custom-model", "api_key": secret})
	if saved.Code != http.StatusOK || strings.Contains(saved.Body.String(), secret) {
		t.Fatalf("save status = %d, body leaked = %s", saved.Code, saved.Body.String())
	}
	updated := adminRequestForTest(t, router, http.MethodPut, path, map[string]string{"workspace_id": developmentWorkspaceID, "base_url": "https://new.example", "model": "new-model", "api_key": ""})
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), secret) {
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
		platformfetch.NewHTTPFetcher(), 0, testAdminToken, configs, modelconfig.Config{Model: "grounded-v1"},
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

func TestVisitorConversationEndpointsDoNotRequireAdminToken(t *testing.T) {
	t.Parallel()
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)
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

func TestKnowledgeQuestionAnswerFlow(t *testing.T) {
	t.Parallel()
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)

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
	router := newRouterWithRepositoriesAndAdminToken(
		testLogger(),
		platformmodel.NewStub(),
		knowledge.NewMemoryRepository(),
		conversation.NewMemoryRepository(),
		fetcher,
		0,
		testAdminToken,
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
			router := newRouterWithRepositoriesAndAdminToken(
				testLogger(),
				platformmodel.NewStub(),
				knowledge.NewMemoryRepository(),
				conversation.NewMemoryRepository(),
				fetcher,
				0,
				testAdminToken,
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

func TestConversationListIsWorkspaceScopedEmptyAndNewestFirst(t *testing.T) {
	t.Parallel()
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)
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
	if len(listed.Conversations) != 2 || listed.Conversations[0].VisitorID != "visitor_new" || listed.Conversations[1].VisitorID != "visitor_old" {
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
	router := newRouterWithAdminToken(testLogger(), platformmodel.NewStub(), testAdminToken)
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

func postAdminJSONForTest(
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
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
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

func getAdminForTest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminRequestForTest(t *testing.T, handler http.Handler, method, path string, payload map[string]string) *httptest.ResponseRecorder {
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
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
