// Package evaluation owns retrieval-only quality test sets and immutable run snapshots.
package evaluation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	"ariad/internal/retrieval"
)

const (
	MinimumTopK = 1
	MaximumTopK = 20
)

var (
	ErrNameRequired           = errors.New("test set name is required")
	ErrQuestionRequired       = errors.New("question is required")
	ErrExpectedSourceRequired = errors.New("expected_source_id is required")
	ErrTestSetNotFound        = errors.New("test set not found")
	ErrTestCaseNotFound       = errors.New("test case not found")
	ErrRunNotFound            = errors.New("evaluation run not found")
	ErrInvalidTopK            = errors.New("top_k must be between 1 and 20")
	ErrInvalidThreshold       = errors.New("threshold must be between 0 and 1")
	ErrEmptyTestSet           = errors.New("test set has no test cases")
	ErrEmbeddingNotConfigured = errors.New("embedding model API key is not configured")
	ErrNoKnowledge            = errors.New("no knowledge sources are available for evaluation")
)

type TestSet struct {
	WorkspaceID, ID, Name string
	Cases                 []TestCase
	CreatedAt, UpdatedAt  time.Time
}
type TestCase struct {
	WorkspaceID, ID, SetID, Question, ExpectedSourceID, Note string
	CreatedAt, UpdatedAt                                     time.Time
}
type Hit struct {
	SourceID    string  `json:"source_id"`
	SourceTitle string  `json:"source_title"`
	Score       float64 `json:"score"`
}
type CaseResult struct {
	CaseID              string `json:"case_id"`
	Question            string `json:"question"`
	ExpectedSourceID    string `json:"expected_source_id"`
	ExpectedSourceTitle string `json:"expected_source_title"`
	Outcome             string `json:"outcome"`
	Hits                []Hit  `json:"hits"`
}
type Run struct {
	WorkspaceID, ID, SetID, SetName                                   string
	TopK, TotalCount, EvaluableCount, PassedCount, SourceMissingCount int
	Threshold, PassRate                                               float64
	Results                                                           []CaseResult
	CreatedAt                                                         time.Time
}

type Repository interface {
	CreateSet(context.Context, TestSet) error
	ListSets(context.Context, string) ([]TestSet, error)
	GetSet(context.Context, string, string) (TestSet, bool, error)
	UpdateSet(context.Context, TestSet) (bool, error)
	DeleteSet(context.Context, string, string) (bool, error)
	CreateCase(context.Context, TestCase) error
	UpdateCase(context.Context, TestCase) (bool, error)
	DeleteCase(context.Context, string, string, string) (bool, error)
	SaveRun(context.Context, Run) error
	ListRuns(context.Context, string, string) ([]Run, error)
	GetRun(context.Context, string, string) (Run, bool, error)
}

type SourceReader interface {
	ListSources(context.Context, string) ([]knowledge.SourceSummary, error)
}

type Service struct {
	repository Repository
	sources    SourceReader
	retriever  retrieval.Retriever
	configs    *modelconfig.Service
	clock      func() time.Time
}

func NewService(repository Repository, sources SourceReader, retriever retrieval.Retriever, configs *modelconfig.Service) *Service {
	return &Service{repository: repository, sources: sources, retriever: retriever, configs: configs, clock: time.Now}
}

func (s *Service) CreateSet(ctx context.Context, workspaceID, name string) (TestSet, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return TestSet{}, ErrNameRequired
	}
	id, err := randomID("evalset")
	if err != nil {
		return TestSet{}, err
	}
	now := s.clock().UTC()
	set := TestSet{WorkspaceID: workspaceID, ID: id, Name: name, Cases: []TestCase{}, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateSet(ctx, set); err != nil {
		return TestSet{}, fmt.Errorf("create test set: %w", err)
	}
	return set, nil
}
func (s *Service) ListSets(ctx context.Context, workspaceID string) ([]TestSet, error) {
	return s.repository.ListSets(ctx, workspaceID)
}
func (s *Service) GetSet(ctx context.Context, workspaceID, setID string) (TestSet, error) {
	v, ok, err := s.repository.GetSet(ctx, workspaceID, setID)
	if err != nil {
		return TestSet{}, err
	}
	if !ok {
		return TestSet{}, ErrTestSetNotFound
	}
	return v, nil
}
func (s *Service) RenameSet(ctx context.Context, workspaceID, setID, name string) (TestSet, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return TestSet{}, ErrNameRequired
	}
	set, err := s.GetSet(ctx, workspaceID, setID)
	if err != nil {
		return TestSet{}, err
	}
	set.Name, set.UpdatedAt = name, s.clock().UTC()
	ok, err := s.repository.UpdateSet(ctx, set)
	if err != nil {
		return TestSet{}, err
	}
	if !ok {
		return TestSet{}, ErrTestSetNotFound
	}
	return set, nil
}
func (s *Service) DeleteSet(ctx context.Context, workspaceID, setID string) error {
	ok, err := s.repository.DeleteSet(ctx, workspaceID, setID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTestSetNotFound
	}
	return nil
}

func validateCase(question, sourceID string) error {
	if strings.TrimSpace(question) == "" {
		return ErrQuestionRequired
	}
	if strings.TrimSpace(sourceID) == "" {
		return ErrExpectedSourceRequired
	}
	return nil
}
func (s *Service) CreateCase(ctx context.Context, workspaceID, setID, question, expectedSourceID, note string) (TestCase, error) {
	if err := validateCase(question, expectedSourceID); err != nil {
		return TestCase{}, err
	}
	if _, err := s.GetSet(ctx, workspaceID, setID); err != nil {
		return TestCase{}, err
	}
	id, err := randomID("evalcase")
	if err != nil {
		return TestCase{}, err
	}
	now := s.clock().UTC()
	item := TestCase{WorkspaceID: workspaceID, ID: id, SetID: setID, Question: strings.TrimSpace(question), ExpectedSourceID: strings.TrimSpace(expectedSourceID), Note: strings.TrimSpace(note), CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateCase(ctx, item); err != nil {
		return TestCase{}, err
	}
	return item, nil
}
func (s *Service) UpdateCase(ctx context.Context, workspaceID, setID, caseID, question, expectedSourceID, note string) (TestCase, error) {
	if err := validateCase(question, expectedSourceID); err != nil {
		return TestCase{}, err
	}
	set, err := s.GetSet(ctx, workspaceID, setID)
	if err != nil {
		return TestCase{}, err
	}
	var item TestCase
	for _, candidate := range set.Cases {
		if candidate.ID == caseID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		return TestCase{}, ErrTestCaseNotFound
	}
	item.Question, item.ExpectedSourceID, item.Note = strings.TrimSpace(question), strings.TrimSpace(expectedSourceID), strings.TrimSpace(note)
	item.UpdatedAt = s.clock().UTC()
	ok, err := s.repository.UpdateCase(ctx, item)
	if err != nil {
		return TestCase{}, err
	}
	if !ok {
		return TestCase{}, ErrTestCaseNotFound
	}
	return item, nil
}
func (s *Service) DeleteCase(ctx context.Context, workspaceID, setID, caseID string) error {
	ok, err := s.repository.DeleteCase(ctx, workspaceID, setID, caseID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTestCaseNotFound
	}
	return nil
}

type RunCommand struct {
	WorkspaceID, SetID string
	TopK               *int
	Threshold          *float64
}

func (s *Service) Run(ctx context.Context, command RunCommand) (Run, error) {
	topK := 5
	if command.TopK != nil {
		topK = *command.TopK
	}
	if topK < MinimumTopK || topK > MaximumTopK {
		return Run{}, ErrInvalidTopK
	}
	if command.Threshold != nil && (*command.Threshold < 0 || *command.Threshold > 1) {
		return Run{}, ErrInvalidThreshold
	}
	status, err := s.configs.GetStatus(ctx, command.WorkspaceID)
	if err != nil {
		return Run{}, err
	}
	if !status.SemanticEnabled || status.EmbeddingAPIKeyMask == "" {
		return Run{}, ErrEmbeddingNotConfigured
	}
	threshold := status.EmbeddingThreshold
	if threshold <= 0 {
		threshold = .35
	}
	if command.Threshold != nil {
		threshold = *command.Threshold
	}
	set, err := s.GetSet(ctx, command.WorkspaceID, command.SetID)
	if err != nil {
		return Run{}, err
	}
	if len(set.Cases) == 0 {
		return Run{}, ErrEmptyTestSet
	}
	sources, err := s.sources.ListSources(ctx, command.WorkspaceID)
	if err != nil {
		return Run{}, err
	}
	if len(sources) == 0 {
		return Run{}, ErrNoKnowledge
	}
	byID := make(map[string]knowledge.SourceSummary, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	results := make([]CaseResult, 0, len(set.Cases))
	passed := 0
	sourceMissing := 0
	for _, item := range set.Cases {
		result := CaseResult{CaseID: item.ID, Question: item.Question, ExpectedSourceID: item.ExpectedSourceID, Outcome: "failed", Hits: []Hit{}}
		expected, exists := byID[item.ExpectedSourceID]
		if !exists {
			result.Outcome = "source_missing"
			sourceMissing++
			results = append(results, result)
			continue
		}
		result.ExpectedSourceTitle = expected.Title
		evidence, retrieveErr := s.retriever.Retrieve(ctx, retrieval.Query{WorkspaceID: command.WorkspaceID, Question: item.Question, Limit: topK, MinimumScore: &threshold})
		if retrieveErr != nil {
			return Run{}, fmt.Errorf("retrieve evaluation case: %w", retrieveErr)
		}
		seen := map[string]int{}
		for _, hit := range evidence {
			if index, ok := seen[hit.SourceID]; ok {
				if hit.Score > result.Hits[index].Score {
					result.Hits[index].Score = hit.Score
				}
				continue
			}
			seen[hit.SourceID] = len(result.Hits)
			result.Hits = append(result.Hits, Hit{SourceID: hit.SourceID, SourceTitle: hit.SourceTitle, Score: hit.Score})
			if hit.SourceID == item.ExpectedSourceID {
				result.Outcome = "passed"
			}
		}
		if result.Outcome == "passed" {
			passed++
		}
		results = append(results, result)
	}
	id, err := randomID("evalrun")
	if err != nil {
		return Run{}, err
	}
	evaluable := len(results) - sourceMissing
	passRate := 0.0
	if evaluable > 0 {
		passRate = float64(passed) / float64(evaluable)
	}
	run := Run{WorkspaceID: command.WorkspaceID, ID: id, SetID: set.ID, SetName: set.Name, TopK: topK, Threshold: threshold, TotalCount: len(results), EvaluableCount: evaluable, PassedCount: passed, SourceMissingCount: sourceMissing, PassRate: passRate, Results: results, CreatedAt: s.clock().UTC()}
	if err := s.repository.SaveRun(ctx, run); err != nil {
		return Run{}, err
	}
	return run, nil
}
func (s *Service) ListRuns(ctx context.Context, workspaceID, setID string) ([]Run, error) {
	if _, err := s.GetSet(ctx, workspaceID, setID); err != nil {
		return nil, err
	}
	return s.repository.ListRuns(ctx, workspaceID, setID)
}
func (s *Service) GetRun(ctx context.Context, workspaceID, runID string) (Run, error) {
	run, ok, err := s.repository.GetRun(ctx, workspaceID, runID)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, ErrRunNotFound
	}
	return run, nil
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}

func sortSets(sets []TestSet) {
	sort.Slice(sets, func(i, j int) bool { return sets[i].UpdatedAt.After(sets[j].UpdatedAt) })
}
