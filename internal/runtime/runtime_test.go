package runtime

import (
	"context"
	"testing"

	"ariad/internal/agent"
	"ariad/internal/retrieval"
)

type fixedRetriever struct {
	evidence []retrieval.Evidence
}

func (r fixedRetriever) Retrieve(context.Context, retrieval.Query) ([]retrieval.Evidence, error) {
	return r.evidence, nil
}

type fixedModel struct {
	response ModelResponse
}

func (m fixedModel) Generate(context.Context, ModelRequest) (ModelResponse, error) {
	return m.response, nil
}

func TestAnswerMapsCitationFromRetrievedEvidence(t *testing.T) {
	t.Parallel()
	evidence := retrieval.Evidence{
		ID:          "ev_server",
		WorkspaceID: "ws_one",
		SourceID:    "src_one",
		ChunkID:     "chk_one",
		SourceTitle: "Policy",
		Text:        "Refunds are available within 30 days.",
	}
	service := NewService(
		publishedAgentReader(),
		fixedRetriever{evidence: []retrieval.Evidence{evidence}},
		fixedModel{response: ModelResponse{
			Text:             "The refund window is 30 days.",
			CitedEvidenceIDs: []string{evidence.ID},
		}},
	)

	result, err := service.Answer(context.Background(), answerCommand())
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if result.TerminalDisposition != DispositionAnswered {
		t.Fatalf("disposition = %q", result.TerminalDisposition)
	}
	if len(result.Citations) != 1 || result.Citations[0].Quote != evidence.Text {
		t.Fatalf("citations = %#v", result.Citations)
	}
}

func TestAnswerRefusesWithoutValidServerEvidence(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		evidence []retrieval.Evidence
		response ModelResponse
	}{
		"no evidence": {},
		"invented citation": {
			evidence: []retrieval.Evidence{{ID: "ev_server", Text: "Known fact"}},
			response: ModelResponse{Text: "Invented answer", CitedEvidenceIDs: []string{"ev_client"}},
		},
		"no citation": {
			evidence: []retrieval.Evidence{{ID: "ev_server", Text: "Known fact"}},
			response: ModelResponse{Text: "Uncited answer"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service := NewService(
				publishedAgentReader(),
				fixedRetriever{evidence: test.evidence},
				fixedModel{response: test.response},
			)
			result, err := service.Answer(context.Background(), answerCommand())
			if err != nil {
				t.Fatalf("answer: %v", err)
			}
			if result.TerminalDisposition != DispositionRefused {
				t.Fatalf("disposition = %q", result.TerminalDisposition)
			}
			if len(result.Citations) != 0 {
				t.Fatalf("refusal citations = %#v", result.Citations)
			}
		})
	}
}

func publishedAgentReader() agent.Reader {
	return agent.NewStaticReader([]agent.PublishedAgent{{
		WorkspaceID: "ws_one",
		AgentID:     "agent_one",
		VersionID:   "version_one",
	}})
}

func answerCommand() AnswerCommand {
	return AnswerCommand{
		WorkspaceID: "ws_one",
		AgentID:     "agent_one",
		Question:    "What is the policy?",
	}
}
