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
	request  *ModelRequest
}

func (m fixedModel) Generate(_ context.Context, request ModelRequest) (ModelResponse, error) {
	if m.request != nil {
		*m.request = request
	}
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

func TestAnswerPassesHistoryWithoutAllowingItAsCitationEvidence(t *testing.T) {
	t.Parallel()
	currentEvidence := retrieval.Evidence{
		ID:          "ev_current",
		WorkspaceID: "ws_one",
		SourceID:    "src_current",
		ChunkID:     "chk_current",
		SourceTitle: "Current source",
		Text:        "Current-turn fact",
	}
	var request ModelRequest
	service := NewService(
		publishedAgentReader(),
		fixedRetriever{evidence: []retrieval.Evidence{currentEvidence}},
		fixedModel{
			request: &request,
			response: ModelResponse{
				Text:             "Grounded answer",
				CitedEvidenceIDs: []string{currentEvidence.ID},
			},
		},
	)
	command := answerCommand()
	command.History = []HistoryTurn{{
		Question: "Earlier question",
		Answer:   "Earlier answer mentioning ev_history",
	}}
	result, err := service.Answer(context.Background(), command)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(request.History) != 1 || request.History[0] != command.History[0] {
		t.Fatalf("model history = %#v, want %#v", request.History, command.History)
	}
	if len(result.Citations) != 1 || result.Citations[0].EvidenceID != currentEvidence.ID {
		t.Fatalf("citations = %#v", result.Citations)
	}

	service = NewService(
		publishedAgentReader(),
		fixedRetriever{evidence: []retrieval.Evidence{currentEvidence}},
		fixedModel{response: ModelResponse{
			Text:             "Answer based on history",
			CitedEvidenceIDs: []string{"ev_history"},
		}},
	)
	result, err = service.Answer(context.Background(), command)
	if err != nil {
		t.Fatalf("answer with history citation: %v", err)
	}
	if result.TerminalDisposition != DispositionRefused || len(result.Citations) != 0 {
		t.Fatalf("history citation result = %#v, want refused without citations", result)
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
