package playground

import (
	"context"
	"errors"
	"testing"

	"ariad/internal/agent"
	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	"ariad/internal/retrieval"
	"ariad/internal/runtime"
)

type fakeChunks struct{ chunks []knowledge.Chunk }

func (f fakeChunks) ListChunks(context.Context, string) ([]knowledge.Chunk, error) {
	return f.chunks, nil
}

type fakeRetriever struct {
	evidence []retrieval.Evidence
	query    retrieval.Query
}

func (f *fakeRetriever) Retrieve(_ context.Context, query retrieval.Query) ([]retrieval.Evidence, error) {
	f.query = query
	return f.evidence, nil
}

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, request runtime.ModelRequest) (runtime.ModelResponse, error) {
	return runtime.ModelResponse{Text: "debug answer", CitedEvidenceIDs: []string{request.Evidence[0].EvidenceID}}, nil
}

func newTestService(chunks []knowledge.Chunk, evidence []retrieval.Evidence, configured bool) (*Service, *fakeRetriever) {
	config := modelconfig.Config{}
	if configured {
		config = modelconfig.Config{BaseURL: "https://model.example", Model: "fake", APIKey: "test-key"}
	}
	retriever := &fakeRetriever{evidence: evidence}
	agents := agent.NewStaticReader([]agent.PublishedAgent{{WorkspaceID: "ws", AgentID: "agent", Instructions: "Use evidence."}})
	return NewService(agents, fakeChunks{chunks: chunks}, retriever, fakeModel{}, modelconfig.NewService(modelconfig.NewMemoryRepository(), config)), retriever
}

func TestAskValidatesParameters(t *testing.T) {
	service, _ := newTestService(nil, nil, true)
	tests := []struct {
		name     string
		command  Command
		expected error
	}{
		{name: "question", command: Command{TopK: 5, Threshold: .3}, expected: ErrInvalidQuestion},
		{name: "top k low", command: Command{Question: "q", TopK: 0, Threshold: .3}, expected: ErrInvalidTopK},
		{name: "top k high", command: Command{Question: "q", TopK: 21, Threshold: .3}, expected: ErrInvalidTopK},
		{name: "threshold", command: Command{Question: "q", TopK: 5, Threshold: 1.1}, expected: ErrInvalidThreshold},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Ask(context.Background(), test.command); !errors.Is(err, test.expected) {
				t.Fatalf("error = %v, want %v", err, test.expected)
			}
		})
	}
}

func TestAskReportsMissingModelAndKnowledge(t *testing.T) {
	unconfigured, _ := newTestService([]knowledge.Chunk{{ID: "chunk"}}, nil, false)
	if _, err := unconfigured.Ask(context.Background(), Command{WorkspaceID: "ws", AgentID: "agent", Question: "q", TopK: 5, Threshold: .3}); !errors.Is(err, ErrModelNotConfigured) {
		t.Fatalf("model error = %v", err)
	}
	empty, _ := newTestService(nil, nil, true)
	if _, err := empty.Ask(context.Background(), Command{WorkspaceID: "ws", AgentID: "agent", Question: "q", TopK: 5, Threshold: .3}); !errors.Is(err, ErrNoKnowledge) {
		t.Fatalf("knowledge error = %v", err)
	}
}

func TestAskPassesPerRequestRetrievalParametersAndReturnsScores(t *testing.T) {
	evidence := []retrieval.Evidence{{ID: "ev_1", SourceID: "src", ChunkID: "chunk", SourceTitle: "Policy", Text: "Refunds take seven days.", Score: .82}}
	service, retriever := newTestService([]knowledge.Chunk{{ID: "chunk"}}, evidence, true)
	result, err := service.Ask(context.Background(), Command{WorkspaceID: "ws", AgentID: "agent", Question: "refund?", TopK: 3, Threshold: .7, Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if retriever.query.Limit != 3 || retriever.query.MinimumScore == nil || *retriever.query.MinimumScore != .7 {
		t.Fatalf("query = %#v", retriever.query)
	}
	if result.Answer != "debug answer" || result.HitCount != 1 || len(result.Hits) != 1 || result.Hits[0].Score != .82 {
		t.Fatalf("result = %#v", result)
	}
}

func TestAskReturnsNoMatchesWithoutCallingModel(t *testing.T) {
	service, _ := newTestService([]knowledge.Chunk{{ID: "chunk"}}, nil, true)
	result, err := service.Ask(context.Background(), Command{WorkspaceID: "ws", AgentID: "agent", Question: "unknown", TopK: 5, Threshold: .9})
	if err != nil || result.Outcome != "no_matches" || result.HitCount != 0 {
		t.Fatalf("result = %#v err = %v", result, err)
	}
}
