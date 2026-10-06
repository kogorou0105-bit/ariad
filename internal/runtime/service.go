package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ariad/internal/agent"
	"ariad/internal/retrieval"
)

const refusalText = "现有知识中没有足够证据回答这个问题。"

// TerminalDisposition is the required terminal state of every answer attempt.
type TerminalDisposition string

const (
	// DispositionAnswered means the response is supported by cited evidence.
	DispositionAnswered TerminalDisposition = "answered"
	// DispositionClarify means the runtime needs more information from the user.
	DispositionClarify TerminalDisposition = "clarify"
	// DispositionRefused means available evidence is insufficient.
	DispositionRefused TerminalDisposition = "refused"
	// DispositionHandoff means the conversation should move to a human workflow.
	DispositionHandoff TerminalDisposition = "handoff"
)

// AnswerCommand contains the authorized context for one answer attempt.
type AnswerCommand struct {
	WorkspaceID string
	AgentID     string
	Question    string
	History     []HistoryTurn
	Locale      string
	RequestID   string
}

// HistoryTurn is a previous question and answer supplied as conversational
// context. It deliberately contains no evidence or citation identifiers.
type HistoryTurn struct {
	Question string
	Answer   string
}

// Citation is mapped by the server from evidence retrieved in the same turn.
type Citation struct {
	EvidenceID  string
	SourceID    string
	ChunkID     string
	SourceTitle string
	Quote       string
}

// ModelUsage contains raw provider units, without prices or billing rules.
type ModelUsage struct {
	Invoked          bool
	Provider         string
	Model            string
	InputUnits       int64
	CachedInputUnits int64
	OutputUnits      int64
}

// Result is the channel-neutral terminal output of the runtime.
type Result struct {
	TerminalDisposition TerminalDisposition
	Text                string
	Citations           []Citation
	ModelUsage          ModelUsage
}

// Answerer runs the answer state machine.
type Answerer interface {
	Answer(ctx context.Context, command AnswerCommand) (Result, error)
}

// ModelEvidence deliberately excludes citation display fields. A model may
// select evidence IDs but cannot author source metadata or quotes.
type ModelEvidence struct {
	EvidenceID string
	Text       string
}

// ModelRequest is the provider-neutral generation request.
type ModelRequest struct {
	WorkspaceID  string
	AgentID      string
	Instructions string
	Question     string
	History      []HistoryTurn
	Locale       string
	Evidence     []ModelEvidence
}

// ModelResponse contains only generated text and selected server evidence IDs.
type ModelResponse struct {
	Text             string
	CitedEvidenceIDs []string
	Usage            ModelUsage
}

// Model is the domain-owned port implemented by provider adapters.
type Model interface {
	Generate(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

// Service coordinates agent configuration, retrieval and model generation.
type Service struct {
	agents    agent.Reader
	retriever retrieval.Retriever
	model     Model
}

// NewService creates an answer runtime.
func NewService(agents agent.Reader, retriever retrieval.Retriever, model Model) *Service {
	return &Service{agents: agents, retriever: retriever, model: model}
}

// Answer returns only terminal outcomes and enforces citation integrity.
func (s *Service) Answer(ctx context.Context, command AnswerCommand) (Result, error) {
	if command.WorkspaceID == "" || command.AgentID == "" || strings.TrimSpace(command.Question) == "" {
		return Result{}, errors.New("workspace, agent and question are required")
	}

	published, err := s.agents.GetPublished(ctx, command.WorkspaceID, command.AgentID)
	if err != nil {
		return Result{}, fmt.Errorf("resolve agent: %w", err)
	}
	evidence, err := s.retriever.Retrieve(ctx, retrieval.Query{
		WorkspaceID: command.WorkspaceID,
		Question:    command.Question,
		Limit:       5,
	})
	if err != nil {
		return Result{}, fmt.Errorf("retrieve evidence: %w", err)
	}
	if len(evidence) == 0 {
		return refusedResult(ModelUsage{}), nil
	}

	modelEvidence := make([]ModelEvidence, 0, len(evidence))
	evidenceByID := make(map[string]retrieval.Evidence, len(evidence))
	for _, item := range evidence {
		modelEvidence = append(modelEvidence, ModelEvidence{EvidenceID: item.ID, Text: item.Text})
		evidenceByID[item.ID] = item
	}
	generated, err := s.model.Generate(ctx, ModelRequest{
		WorkspaceID:  command.WorkspaceID,
		AgentID:      command.AgentID,
		Instructions: published.Instructions,
		Question:     command.Question,
		History:      append([]HistoryTurn(nil), command.History...),
		Locale:       command.Locale,
		Evidence:     modelEvidence,
	})
	if err != nil {
		return Result{}, fmt.Errorf("generate answer: %w", err)
	}

	citations := make([]Citation, 0, len(generated.CitedEvidenceIDs))
	seen := make(map[string]struct{}, len(generated.CitedEvidenceIDs))
	for _, evidenceID := range generated.CitedEvidenceIDs {
		item, ok := evidenceByID[evidenceID]
		if !ok {
			return refusedResult(generated.Usage), nil
		}
		if _, duplicated := seen[evidenceID]; duplicated {
			continue
		}
		seen[evidenceID] = struct{}{}
		citations = append(citations, Citation{
			EvidenceID:  item.ID,
			SourceID:    item.SourceID,
			ChunkID:     item.ChunkID,
			SourceTitle: item.SourceTitle,
			Quote:       item.Text,
		})
	}
	if strings.TrimSpace(generated.Text) == "" || len(citations) == 0 {
		return refusedResult(generated.Usage), nil
	}

	return Result{
		TerminalDisposition: DispositionAnswered,
		Text:                generated.Text,
		Citations:           citations,
		ModelUsage:          generated.Usage,
	}, nil
}

func refusedResult(modelUsage ModelUsage) Result {
	return Result{
		TerminalDisposition: DispositionRefused,
		Text:                refusalText,
		Citations:           []Citation{},
		ModelUsage:          modelUsage,
	}
}
