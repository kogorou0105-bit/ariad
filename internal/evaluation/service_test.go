package evaluation

import (
	"context"
	"errors"
	"testing"

	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	"ariad/internal/retrieval"
)

type fakeSources struct{ values []knowledge.SourceSummary }

func (f fakeSources) ListSources(context.Context, string) ([]knowledge.SourceSummary, error) {
	return f.values, nil
}

type fakeRetriever struct {
	values  map[string][]retrieval.Evidence
	queries []retrieval.Query
}

func (f *fakeRetriever) Retrieve(_ context.Context, q retrieval.Query) ([]retrieval.Evidence, error) {
	f.queries = append(f.queries, q)
	return f.values[q.Question], nil
}

func configuredService(t *testing.T, sources []knowledge.SourceSummary, retriever *fakeRetriever) (*Service, *MemoryRepository) {
	t.Helper()
	configs := modelconfig.NewMemoryRepository()
	err := configs.Save(context.Background(), modelconfig.Config{WorkspaceID: "ws", BaseURL: "https://example.com", Model: "chat", APIKey: "chat-key", EmbeddingBaseURL: "https://example.com", EmbeddingModel: "embed", EmbeddingAPIKey: "embed-key", EmbeddingThreshold: .4})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewMemoryRepository()
	return NewService(repo, fakeSources{sources}, retriever, modelconfig.NewService(configs, modelconfig.Config{})), repo
}
func source(id, title string) knowledge.SourceSummary {
	return knowledge.SourceSummary{Source: knowledge.Source{ID: id, Title: title, Status: "ready"}, ChunkCount: 1}
}

func TestRunJudgesHitsNoHitsAndMissingSources(t *testing.T) {
	retriever := &fakeRetriever{values: map[string][]retrieval.Evidence{"hit": {{SourceID: "wanted", SourceTitle: "Expected", Score: .91}}, "miss": {{SourceID: "other", SourceTitle: "Other", Score: .8}}}}
	service, _ := configuredService(t, []knowledge.SourceSummary{source("wanted", "Expected"), source("other", "Other")}, retriever)
	set, err := service.CreateSet(context.Background(), "ws", "Regression")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ question, source string }{{"hit", "wanted"}, {"miss", "wanted"}, {"none", "wanted"}, {"missing", "deleted"}} {
		if _, err := service.CreateCase(context.Background(), "ws", set.ID, v.question, v.source, ""); err != nil {
			t.Fatal(err)
		}
	}
	topK, threshold := 3, .6
	run, err := service.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: set.ID, TopK: &topK, Threshold: &threshold})
	if err != nil {
		t.Fatal(err)
	}
	if run.PassedCount != 1 || run.TotalCount != 4 || run.EvaluableCount != 3 || run.SourceMissingCount != 1 || run.PassRate != 1.0/3.0 {
		t.Fatalf("unexpected aggregate: %+v", run)
	}
	want := []string{"passed", "failed", "failed", "source_missing"}
	for i, result := range run.Results {
		if result.Outcome != want[i] {
			t.Errorf("result %d = %s, want %s", i, result.Outcome, want[i])
		}
	}
	if len(retriever.queries) != 3 {
		t.Fatalf("retrieval calls = %d, want 3", len(retriever.queries))
	}
	for _, q := range retriever.queries {
		if q.Limit != 3 || q.MinimumScore == nil || *q.MinimumScore != .6 {
			t.Errorf("query params = %+v", q)
		}
	}
}

func TestRunValidationAndConfigurationErrors(t *testing.T) {
	service, _ := configuredService(t, []knowledge.SourceSummary{source("s", "S")}, &fakeRetriever{values: map[string][]retrieval.Evidence{}})
	set, _ := service.CreateSet(context.Background(), "ws", "Set")
	if _, err := service.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: set.ID}); !errors.Is(err, ErrEmptyTestSet) {
		t.Fatalf("empty err = %v", err)
	}
	if _, err := service.CreateCase(context.Background(), "ws", set.ID, "q", "s", ""); err != nil {
		t.Fatal(err)
	}
	badK := 0
	if _, err := service.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: set.ID, TopK: &badK}); !errors.Is(err, ErrInvalidTopK) {
		t.Fatalf("top-k err = %v", err)
	}
	badThreshold := 1.1
	if _, err := service.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: set.ID, Threshold: &badThreshold}); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("threshold err = %v", err)
	}
	unconfigured := NewService(NewMemoryRepository(), fakeSources{[]knowledge.SourceSummary{source("s", "S")}}, &fakeRetriever{}, modelconfig.NewService(modelconfig.NewMemoryRepository(), modelconfig.Config{}))
	if _, err := unconfigured.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: "x"}); !errors.Is(err, ErrEmbeddingNotConfigured) {
		t.Fatalf("config err = %v", err)
	}
}

func TestRunWithOnlyMissingSourcesHasNoEvaluableCases(t *testing.T) {
	service, _ := configuredService(t, []knowledge.SourceSummary{source("remaining", "Remaining")}, &fakeRetriever{values: map[string][]retrieval.Evidence{}})
	set, err := service.CreateSet(context.Background(), "ws", "Missing sources")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateCase(context.Background(), "ws", set.ID, "q", "deleted", ""); err != nil {
		t.Fatal(err)
	}
	run, err := service.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: set.ID})
	if err != nil {
		t.Fatal(err)
	}
	if run.EvaluableCount != 0 || run.SourceMissingCount != 1 || run.PassRate != 0 {
		t.Fatalf("run = %+v", run)
	}
}

func TestCRUDNotFoundErrorsAndRunHistory(t *testing.T) {
	service, _ := configuredService(t, []knowledge.SourceSummary{source("s", "S")}, &fakeRetriever{values: map[string][]retrieval.Evidence{"q": {{SourceID: "s", Score: 1}}}})
	if _, err := service.CreateSet(context.Background(), "ws", " "); !errors.Is(err, ErrNameRequired) {
		t.Fatal(err)
	}
	if err := service.DeleteSet(context.Background(), "ws", "missing"); !errors.Is(err, ErrTestSetNotFound) {
		t.Fatal(err)
	}
	set, _ := service.CreateSet(context.Background(), "ws", "Before")
	set, err := service.RenameSet(context.Background(), "ws", set.ID, "After")
	if err != nil || set.Name != "After" {
		t.Fatalf("rename = %+v %v", set, err)
	}
	item, _ := service.CreateCase(context.Background(), "ws", set.ID, "q", "s", "note")
	createdAt := item.CreatedAt
	item, err = service.UpdateCase(context.Background(), "ws", set.ID, item.ID, "q2", "s", "changed")
	if err != nil || item.Note != "changed" || !item.CreatedAt.Equal(createdAt) {
		t.Fatalf("update = %+v %v", item, err)
	}
	item, _ = service.UpdateCase(context.Background(), "ws", set.ID, item.ID, "q", "s", "changed")
	run, err := service.Run(context.Background(), RunCommand{WorkspaceID: "ws", SetID: set.ID})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := service.ListRuns(context.Background(), "ws", set.ID)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs = %+v %v", runs, err)
	}
	saved, err := service.GetRun(context.Background(), "ws", run.ID)
	if err != nil || saved.SetName != "After" {
		t.Fatalf("saved = %+v %v", saved, err)
	}
}
