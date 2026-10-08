package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"ariad/internal/conversation"
	"ariad/internal/ingestion"
	"ariad/internal/knowledge"
	platformfetch "ariad/internal/platform/fetch"
	platformmodel "ariad/internal/platform/model"
)

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
