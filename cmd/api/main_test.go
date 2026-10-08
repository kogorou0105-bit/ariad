package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"ariad/internal/ingestion"
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
