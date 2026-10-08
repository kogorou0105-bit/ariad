package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbgen "ariad/db/generated"
	"ariad/internal/evaluation"
)

type EvaluationRepository struct {
	database *sql.DB
	queries  *dbgen.Queries
}

var _ evaluation.Repository = (*EvaluationRepository)(nil)

func NewEvaluationRepository(database *sql.DB) *EvaluationRepository {
	return &EvaluationRepository{database: database, queries: dbgen.New(database)}
}
func (r *EvaluationRepository) CreateSet(ctx context.Context, s evaluation.TestSet) error {
	return r.queries.CreateEvaluationTestSet(ctx, dbgen.CreateEvaluationTestSetParams{WorkspaceID: s.WorkspaceID, SetID: s.ID, Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
}
func (r *EvaluationRepository) ListSets(ctx context.Context, w string) ([]evaluation.TestSet, error) {
	rows, e := r.queries.ListEvaluationTestSets(ctx, w)
	if e != nil {
		return nil, e
	}
	out := make([]evaluation.TestSet, 0, len(rows))
	for _, row := range rows {
		s := evaluation.TestSet{WorkspaceID: row.WorkspaceID, ID: row.SetID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		s.Cases, e = r.listCases(ctx, w, s.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, nil
}
func (r *EvaluationRepository) GetSet(ctx context.Context, w, id string) (evaluation.TestSet, bool, error) {
	row, e := r.queries.GetEvaluationTestSet(ctx, dbgen.GetEvaluationTestSetParams{WorkspaceID: w, SetID: id})
	if errors.Is(e, sql.ErrNoRows) {
		return evaluation.TestSet{}, false, nil
	}
	if e != nil {
		return evaluation.TestSet{}, false, e
	}
	s := evaluation.TestSet{WorkspaceID: row.WorkspaceID, ID: row.SetID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	s.Cases, e = r.listCases(ctx, w, id)
	return s, e == nil, e
}
func (r *EvaluationRepository) listCases(ctx context.Context, w, id string) ([]evaluation.TestCase, error) {
	rows, e := r.queries.ListEvaluationTestCases(ctx, dbgen.ListEvaluationTestCasesParams{WorkspaceID: w, SetID: id})
	if e != nil {
		return nil, e
	}
	out := make([]evaluation.TestCase, 0, len(rows))
	for _, v := range rows {
		out = append(out, evaluation.TestCase{WorkspaceID: v.WorkspaceID, ID: v.CaseID, SetID: v.SetID, Question: v.Question, ExpectedSourceID: v.ExpectedSourceID, Note: v.Note, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	return out, nil
}
func (r *EvaluationRepository) UpdateSet(ctx context.Context, s evaluation.TestSet) (bool, error) {
	n, e := r.queries.UpdateEvaluationTestSet(ctx, dbgen.UpdateEvaluationTestSetParams{Name: s.Name, UpdatedAt: s.UpdatedAt, WorkspaceID: s.WorkspaceID, SetID: s.ID})
	return n > 0, e
}
func (r *EvaluationRepository) DeleteSet(ctx context.Context, w, id string) (bool, error) {
	n, e := r.queries.DeleteEvaluationTestSet(ctx, dbgen.DeleteEvaluationTestSetParams{WorkspaceID: w, SetID: id})
	return n > 0, e
}
func (r *EvaluationRepository) CreateCase(ctx context.Context, v evaluation.TestCase) error {
	return r.queries.CreateEvaluationTestCase(ctx, dbgen.CreateEvaluationTestCaseParams{WorkspaceID: v.WorkspaceID, CaseID: v.ID, SetID: v.SetID, Question: v.Question, ExpectedSourceID: v.ExpectedSourceID, Note: v.Note, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
}
func (r *EvaluationRepository) UpdateCase(ctx context.Context, v evaluation.TestCase) (bool, error) {
	n, e := r.queries.UpdateEvaluationTestCase(ctx, dbgen.UpdateEvaluationTestCaseParams{Question: v.Question, ExpectedSourceID: v.ExpectedSourceID, Note: v.Note, UpdatedAt: v.UpdatedAt, WorkspaceID: v.WorkspaceID, SetID: v.SetID, CaseID: v.ID})
	return n > 0, e
}
func (r *EvaluationRepository) DeleteCase(ctx context.Context, w, setID, caseID string) (bool, error) {
	n, e := r.queries.DeleteEvaluationTestCase(ctx, dbgen.DeleteEvaluationTestCaseParams{WorkspaceID: w, SetID: setID, CaseID: caseID})
	return n > 0, e
}
func (r *EvaluationRepository) SaveRun(ctx context.Context, v evaluation.Run) error {
	raw, e := json.Marshal(v.Results)
	if e != nil {
		return e
	}
	topK, e := checkedInt32(v.TopK)
	if e != nil {
		return e
	}
	total, e := checkedInt32(v.TotalCount)
	if e != nil {
		return e
	}
	evaluable, e := checkedInt32(v.EvaluableCount)
	if e != nil {
		return e
	}
	passed, e := checkedInt32(v.PassedCount)
	if e != nil {
		return e
	}
	missing, e := checkedInt32(v.SourceMissingCount)
	if e != nil {
		return e
	}
	return r.queries.CreateEvaluationRun(ctx, dbgen.CreateEvaluationRunParams{WorkspaceID: v.WorkspaceID, RunID: v.ID, SetID: v.SetID, SetName: v.SetName, TopK: topK, Threshold: v.Threshold, TotalCount: total, EvaluableCount: evaluable, PassedCount: passed, SourceMissingCount: missing, PassRate: v.PassRate, Results: raw, CreatedAt: v.CreatedAt})
}
func runFromDB(workspaceID, runID, setID, setName string, topK int32, threshold float64, total, evaluable, passed, missing int32, passRate float64, raw []byte, createdAt time.Time) (evaluation.Run, error) {
	var results []evaluation.CaseResult
	if e := json.Unmarshal(raw, &results); e != nil {
		return evaluation.Run{}, fmt.Errorf("decode evaluation results: %w", e)
	}
	return evaluation.Run{WorkspaceID: workspaceID, ID: runID, SetID: setID, SetName: setName, TopK: int(topK), Threshold: threshold, TotalCount: int(total), EvaluableCount: int(evaluable), PassedCount: int(passed), SourceMissingCount: int(missing), PassRate: passRate, Results: results, CreatedAt: createdAt}, nil
}
func (r *EvaluationRepository) ListRuns(ctx context.Context, w, setID string) ([]evaluation.Run, error) {
	rows, e := r.queries.ListEvaluationRuns(ctx, dbgen.ListEvaluationRunsParams{WorkspaceID: w, SetID: setID})
	if e != nil {
		return nil, e
	}
	out := make([]evaluation.Run, 0, len(rows))
	for _, v := range rows {
		run, err := runFromDB(v.WorkspaceID, v.RunID, v.SetID, v.SetName, v.TopK, v.Threshold, v.TotalCount, v.EvaluableCount, v.PassedCount, v.SourceMissingCount, v.PassRate, v.Results, v.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}
func (r *EvaluationRepository) GetRun(ctx context.Context, w, id string) (evaluation.Run, bool, error) {
	v, e := r.queries.GetEvaluationRun(ctx, dbgen.GetEvaluationRunParams{WorkspaceID: w, RunID: id})
	if errors.Is(e, sql.ErrNoRows) {
		return evaluation.Run{}, false, nil
	}
	if e != nil {
		return evaluation.Run{}, false, e
	}
	run, e := runFromDB(v.WorkspaceID, v.RunID, v.SetID, v.SetName, v.TopK, v.Threshold, v.TotalCount, v.EvaluableCount, v.PassedCount, v.SourceMissingCount, v.PassRate, v.Results, v.CreatedAt)
	return run, e == nil, e
}
