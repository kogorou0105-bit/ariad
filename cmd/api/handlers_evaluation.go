package main

import (
	"errors"
	"net/http"
	"time"

	"ariad/internal/evaluation"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type evaluationSetRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
}
type evaluationCaseRequest struct {
	WorkspaceID      string `json:"workspace_id"`
	Question         string `json:"question"`
	ExpectedSourceID string `json:"expected_source_id"`
	Note             string `json:"note"`
}
type evaluationRunRequest struct {
	WorkspaceID string   `json:"workspace_id"`
	TopK        *int     `json:"top_k"`
	Threshold   *float64 `json:"threshold"`
}
type evaluationCaseResponse struct {
	CaseID           string    `json:"case_id"`
	Question         string    `json:"question"`
	ExpectedSourceID string    `json:"expected_source_id"`
	Note             string    `json:"note"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type evaluationSetResponse struct {
	SetID     string                   `json:"set_id"`
	Name      string                   `json:"name"`
	Cases     []evaluationCaseResponse `json:"cases"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
}
type evaluationHitResponse struct {
	SourceID    string  `json:"source_id"`
	SourceTitle string  `json:"source_title"`
	Score       float64 `json:"score"`
}
type evaluationCaseResultResponse struct {
	CaseID              string                  `json:"case_id"`
	Question            string                  `json:"question"`
	ExpectedSourceID    string                  `json:"expected_source_id"`
	ExpectedSourceTitle string                  `json:"expected_source_title"`
	Outcome             string                  `json:"outcome"`
	Hits                []evaluationHitResponse `json:"hits"`
}
type evaluationRunResponse struct {
	RunID              string                         `json:"run_id"`
	SetID              string                         `json:"set_id"`
	SetName            string                         `json:"set_name"`
	TopK               int                            `json:"top_k"`
	Threshold          float64                        `json:"threshold"`
	TotalCount         int                            `json:"total_count"`
	EvaluableCount     int                            `json:"evaluable_count"`
	PassedCount        int                            `json:"passed_count"`
	SourceMissingCount int                            `json:"source_missing_count"`
	PassRate           float64                        `json:"pass_rate"`
	Results            []evaluationCaseResultResponse `json:"results"`
	CreatedAt          time.Time                      `json:"created_at"`
}

func setResponse(v evaluation.TestSet) evaluationSetResponse {
	cases := make([]evaluationCaseResponse, 0, len(v.Cases))
	for _, c := range v.Cases {
		cases = append(cases, evaluationCaseResponse{CaseID: c.ID, Question: c.Question, ExpectedSourceID: c.ExpectedSourceID, Note: c.Note, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
	}
	return evaluationSetResponse{SetID: v.ID, Name: v.Name, Cases: cases, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func runResponse(v evaluation.Run) evaluationRunResponse {
	results := make([]evaluationCaseResultResponse, 0, len(v.Results))
	for _, r := range v.Results {
		hits := make([]evaluationHitResponse, 0, len(r.Hits))
		for _, h := range r.Hits {
			hits = append(hits, evaluationHitResponse{SourceID: h.SourceID, SourceTitle: h.SourceTitle, Score: h.Score})
		}
		results = append(results, evaluationCaseResultResponse{CaseID: r.CaseID, Question: r.Question, ExpectedSourceID: r.ExpectedSourceID, ExpectedSourceTitle: r.ExpectedSourceTitle, Outcome: r.Outcome, Hits: hits})
	}
	return evaluationRunResponse{RunID: v.ID, SetID: v.SetID, SetName: v.SetName, TopK: v.TopK, Threshold: v.Threshold, TotalCount: v.TotalCount, EvaluableCount: v.EvaluableCount, PassedCount: v.PassedCount, SourceMissingCount: v.SourceMissingCount, PassRate: v.PassRate, Results: results, CreatedAt: v.CreatedAt}
}
func (h apiHandlers) evaluationWorkspace(response http.ResponseWriter, request *http.Request, value string) bool {
	if value != h.scope.workspaceID {
		h.writeError(response, http.StatusForbidden, "workspace_not_allowed", "unknown workspace", middleware.GetReqID(request.Context()))
		return false
	}
	return true
}
func (h apiHandlers) evaluationError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "evaluation_failed"
	switch {
	case errors.Is(err, evaluation.ErrNameRequired), errors.Is(err, evaluation.ErrQuestionRequired), errors.Is(err, evaluation.ErrExpectedSourceRequired), errors.Is(err, evaluation.ErrInvalidTopK), errors.Is(err, evaluation.ErrInvalidThreshold):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, evaluation.ErrTestSetNotFound), errors.Is(err, evaluation.ErrTestCaseNotFound), errors.Is(err, evaluation.ErrRunNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, evaluation.ErrEmbeddingNotConfigured):
		status, code = http.StatusPreconditionFailed, "embedding_not_configured"
	case errors.Is(err, evaluation.ErrEmptyTestSet):
		status, code = http.StatusUnprocessableEntity, "empty_test_set"
	case errors.Is(err, evaluation.ErrNoKnowledge):
		status, code = http.StatusUnprocessableEntity, "no_knowledge"
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "could not complete evaluation request"
	}
	h.writeError(w, status, code, message, middleware.GetReqID(r.Context()))
}
func (h apiHandlers) evaluationDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), middleware.GetReqID(r.Context()))
}
func (h apiHandlers) listEvaluationTestSets(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_id")
	if !h.evaluationWorkspace(w, r, workspace) {
		return
	}
	sets, err := h.evaluation.ListSets(r.Context(), workspace)
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	out := make([]evaluationSetResponse, 0, len(sets))
	for _, s := range sets {
		out = append(out, setResponse(s))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspace, "test_sets": out})
}
func (h apiHandlers) createEvaluationTestSet(w http.ResponseWriter, r *http.Request) {
	var in evaluationSetRequest
	if err := decodeJSON(w, r, maximumQuestionBody, &in); err != nil {
		h.evaluationDecodeError(w, r, err)
		return
	}
	if !h.evaluationWorkspace(w, r, in.WorkspaceID) {
		return
	}
	v, err := h.evaluation.CreateSet(r.Context(), in.WorkspaceID, in.Name)
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, setResponse(v))
}
func (h apiHandlers) getEvaluationTestSet(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_id")
	if !h.evaluationWorkspace(w, r, workspace) {
		return
	}
	v, err := h.evaluation.GetSet(r.Context(), workspace, chi.URLParam(r, "set_id"))
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, setResponse(v))
}
func (h apiHandlers) renameEvaluationTestSet(w http.ResponseWriter, r *http.Request) {
	var in evaluationSetRequest
	if err := decodeJSON(w, r, maximumQuestionBody, &in); err != nil {
		h.evaluationDecodeError(w, r, err)
		return
	}
	if !h.evaluationWorkspace(w, r, in.WorkspaceID) {
		return
	}
	v, err := h.evaluation.RenameSet(r.Context(), in.WorkspaceID, chi.URLParam(r, "set_id"), in.Name)
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, setResponse(v))
}
func (h apiHandlers) deleteEvaluationTestSet(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_id")
	if !h.evaluationWorkspace(w, r, workspace) {
		return
	}
	err := h.evaluation.DeleteSet(r.Context(), workspace, chi.URLParam(r, "set_id"))
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
func (h apiHandlers) createEvaluationTestCase(w http.ResponseWriter, r *http.Request) {
	var in evaluationCaseRequest
	if err := decodeJSON(w, r, maximumQuestionBody, &in); err != nil {
		h.evaluationDecodeError(w, r, err)
		return
	}
	if !h.evaluationWorkspace(w, r, in.WorkspaceID) {
		return
	}
	v, err := h.evaluation.CreateCase(r.Context(), in.WorkspaceID, chi.URLParam(r, "set_id"), in.Question, in.ExpectedSourceID, in.Note)
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, evaluationCaseResponse{CaseID: v.ID, Question: v.Question, ExpectedSourceID: v.ExpectedSourceID, Note: v.Note, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
}
func (h apiHandlers) updateEvaluationTestCase(w http.ResponseWriter, r *http.Request) {
	var in evaluationCaseRequest
	if err := decodeJSON(w, r, maximumQuestionBody, &in); err != nil {
		h.evaluationDecodeError(w, r, err)
		return
	}
	if !h.evaluationWorkspace(w, r, in.WorkspaceID) {
		return
	}
	v, err := h.evaluation.UpdateCase(r.Context(), in.WorkspaceID, chi.URLParam(r, "set_id"), chi.URLParam(r, "case_id"), in.Question, in.ExpectedSourceID, in.Note)
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, evaluationCaseResponse{CaseID: v.ID, Question: v.Question, ExpectedSourceID: v.ExpectedSourceID, Note: v.Note, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
}
func (h apiHandlers) deleteEvaluationTestCase(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_id")
	if !h.evaluationWorkspace(w, r, workspace) {
		return
	}
	err := h.evaluation.DeleteCase(r.Context(), workspace, chi.URLParam(r, "set_id"), chi.URLParam(r, "case_id"))
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
func (h apiHandlers) runEvaluation(w http.ResponseWriter, r *http.Request) {
	var in evaluationRunRequest
	if err := decodeJSON(w, r, maximumQuestionBody, &in); err != nil {
		h.evaluationDecodeError(w, r, err)
		return
	}
	if !h.evaluationWorkspace(w, r, in.WorkspaceID) {
		return
	}
	v, err := h.evaluation.Run(r.Context(), evaluation.RunCommand{WorkspaceID: in.WorkspaceID, SetID: chi.URLParam(r, "set_id"), TopK: in.TopK, Threshold: in.Threshold})
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, runResponse(v))
}
func (h apiHandlers) listEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_id")
	if !h.evaluationWorkspace(w, r, workspace) {
		return
	}
	runs, err := h.evaluation.ListRuns(r.Context(), workspace, chi.URLParam(r, "set_id"))
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	out := make([]evaluationRunResponse, 0, len(runs))
	for _, run := range runs {
		out = append(out, runResponse(run))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspace, "runs": out})
}
func (h apiHandlers) getEvaluationRun(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_id")
	if !h.evaluationWorkspace(w, r, workspace) {
		return
	}
	run, err := h.evaluation.GetRun(r.Context(), workspace, chi.URLParam(r, "run_id"))
	if err != nil {
		h.evaluationError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, runResponse(run))
}
