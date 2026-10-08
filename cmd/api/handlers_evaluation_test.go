package main

import (
	"encoding/json"
	"net/http"
	"testing"

	platformmodel "ariad/internal/platform/model"
)

func TestEvaluationCRUDAndErrors(t *testing.T) {
	handler := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	created := adminRequestForTest(t, handler, http.MethodPost, "/api/v1/evaluation/test-sets", map[string]any{"workspace_id": developmentWorkspaceID, "name": "Regression"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var set evaluationSetResponse
	if err := json.Unmarshal(created.Body.Bytes(), &set); err != nil {
		t.Fatal(err)
	}
	if set.Name != "Regression" || set.SetID == "" {
		t.Fatalf("set = %+v", set)
	}

	listed := getAdminForTest(t, handler, "/api/v1/evaluation/test-sets?workspace_id="+developmentWorkspaceID)
	if listed.Code != http.StatusOK || !json.Valid(listed.Body.Bytes()) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}

	invalid := adminRequestForTest(t, handler, http.MethodPost, "/api/v1/evaluation/test-sets/"+set.SetID+"/cases", map[string]any{"workspace_id": developmentWorkspaceID, "question": "", "expected_source_id": "source"})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid case = %d %s", invalid.Code, invalid.Body.String())
	}
	missing := adminRequestForTest(t, handler, http.MethodPut, "/api/v1/evaluation/test-sets/"+set.SetID+"/cases/missing", map[string]any{"workspace_id": developmentWorkspaceID, "question": "Q", "expected_source_id": "source"})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing case = %d %s", missing.Code, missing.Body.String())
	}

	emptyRun := adminRequestForTest(t, handler, http.MethodPost, "/api/v1/evaluation/test-sets/"+set.SetID+"/runs", map[string]any{"workspace_id": developmentWorkspaceID, "top_k": 5, "threshold": .35})
	if emptyRun.Code != http.StatusPreconditionFailed {
		t.Fatalf("unconfigured run = %d %s", emptyRun.Code, emptyRun.Body.String())
	}

	deleted := adminRequestForTest(t, handler, http.MethodDelete, "/api/v1/evaluation/test-sets/"+set.SetID+"?workspace_id="+developmentWorkspaceID, nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	notFound := getAdminForTest(t, handler, "/api/v1/evaluation/test-sets/"+set.SetID+"?workspace_id="+developmentWorkspaceID)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("get deleted = %d %s", notFound.Code, notFound.Body.String())
	}
}

func TestEvaluationRequiresAdminAndWorkspaceScope(t *testing.T) {
	handler := newRouterWithBootstrapPassword(testLogger(), platformmodel.NewStub(), testAdminPassword)
	unauthorized := getForTest(t, handler, "/api/v1/evaluation/test-sets?workspace_id="+developmentWorkspaceID)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", unauthorized.Code)
	}
	forbidden := getAdminForTest(t, handler, "/api/v1/evaluation/test-sets?workspace_id=other")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden = %d %s", forbidden.Code, forbidden.Body.String())
	}
}
