package conversation

import (
	"context"
	"errors"
	"testing"

	"ariad/internal/runtime"
	"ariad/internal/usage"
)

type fixedAnswerer struct {
	result runtime.Result
}

func (a fixedAnswerer) Answer(context.Context, runtime.AnswerCommand) (runtime.Result, error) {
	return a.result, nil
}

type recordingRepository struct {
	Repository
	facts []usage.Fact
}

func (r *recordingRepository) SaveTurn(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	turn Turn,
	fact usage.Fact,
) (Turn, error) {
	r.facts = append(r.facts, fact)
	return r.Repository.SaveTurn(
		ctx,
		workspaceID,
		idempotencyKey,
		payloadFingerprint,
		turn,
		fact,
	)
}

func TestSubmitQuestionRecordsUsageAndStableIDs(t *testing.T) {
	t.Parallel()
	repository := &recordingRepository{Repository: NewMemoryRepository()}
	service := NewService(
		repository,
		fixedAnswerer{result: runtime.Result{
			TerminalDisposition: runtime.DispositionAnswered,
			Text:                "Grounded answer",
			Citations: []runtime.Citation{{
				EvidenceID: "ev_one",
				SourceID:   "src_one",
				ChunkID:    "chk_one",
				Quote:      "Grounded fact",
			}},
			ModelUsage: runtime.ModelUsage{Invoked: true, Provider: "stub", OutputUnits: 4},
		}},
	)
	command := SubmitQuestionCommand{
		WorkspaceID:    "ws_one",
		AgentID:        "agent_one",
		VisitorID:      "visitor_one",
		Channel:        ChannelWidget,
		RequestID:      "req_one",
		IdempotencyKey: "ik_one",
		Question:       "Question?",
	}

	first, err := service.SubmitQuestion(context.Background(), command)
	if err != nil {
		t.Fatalf("submit question: %v", err)
	}
	second, err := service.SubmitQuestion(context.Background(), command)
	if err != nil {
		t.Fatalf("replay question: %v", err)
	}
	if first.Message.ID != second.Message.ID || first.Answer.ID != second.Answer.ID {
		t.Fatalf("replay changed stable IDs: %#v then %#v", first, second)
	}
	if len(repository.facts) != 1 {
		t.Fatalf("usage facts = %d, want 1", len(repository.facts))
	}
	if repository.facts[0].WorkspaceID != command.WorkspaceID ||
		repository.facts[0].AnswerID != first.Answer.ID ||
		repository.facts[0].Status != usage.StatusCompleted {
		t.Fatalf("usage fact = %#v", repository.facts[0])
	}
}

func TestSubmitQuestionRejectsIdempotencyKeyWithDifferentPayload(t *testing.T) {
	t.Parallel()
	repository := &recordingRepository{Repository: NewMemoryRepository()}
	service := NewService(
		repository,
		fixedAnswerer{result: runtime.Result{
			TerminalDisposition: runtime.DispositionRefused,
			Text:                "Not enough evidence",
		}},
	)
	command := SubmitQuestionCommand{
		WorkspaceID:    "ws_one",
		AgentID:        "agent_one",
		VisitorID:      "visitor_one",
		Channel:        ChannelWidget,
		RequestID:      "req_one",
		IdempotencyKey: "ik_one",
		Question:       "Original question?",
	}
	if _, err := service.SubmitQuestion(context.Background(), command); err != nil {
		t.Fatalf("submit question: %v", err)
	}
	command.Question = "Different question?"
	if _, err := service.SubmitQuestion(context.Background(), command); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
	}
	if len(repository.facts) != 1 {
		t.Fatalf("usage facts = %d, want 1", len(repository.facts))
	}
}

func TestConversationCannotBeReusedByAnotherVisitor(t *testing.T) {
	t.Parallel()
	service := NewService(
		NewMemoryRepository(),
		fixedAnswerer{result: runtime.Result{
			TerminalDisposition: runtime.DispositionRefused,
			Text:                "Not enough evidence",
		}},
	)
	first, err := service.SubmitQuestion(context.Background(), SubmitQuestionCommand{
		WorkspaceID:    "ws_one",
		AgentID:        "agent_one",
		VisitorID:      "visitor_one",
		Channel:        ChannelWidget,
		RequestID:      "req_one",
		IdempotencyKey: "ik_one",
		Question:       "Question?",
	})
	if err != nil {
		t.Fatalf("submit first question: %v", err)
	}
	_, err = service.SubmitQuestion(context.Background(), SubmitQuestionCommand{
		WorkspaceID:    "ws_one",
		AgentID:        "agent_one",
		ConversationID: first.Message.ConversationID,
		VisitorID:      "visitor_two",
		Channel:        ChannelWidget,
		RequestID:      "req_two",
		IdempotencyKey: "ik_two",
		Question:       "Question?",
	})
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("error = %v, want ErrConversationNotFound", err)
	}
}
