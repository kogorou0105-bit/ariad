package conversation

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"ariad/internal/runtime"
	"ariad/internal/usage"
)

type fixedAnswerer struct {
	result runtime.Result
}

type recordingAnswerer struct {
	commands []runtime.AnswerCommand
	result   runtime.Result
}

func (a *recordingAnswerer) Answer(
	_ context.Context,
	command runtime.AnswerCommand,
) (runtime.Result, error) {
	a.commands = append(a.commands, command)
	return a.result, nil
}

func (a fixedAnswerer) Answer(context.Context, runtime.AnswerCommand) (runtime.Result, error) {
	return a.result, nil
}

type recordingRepository struct {
	Repository
	facts []usage.Fact
}

type recordingHandoffs struct {
	calls int
	event StateEvent
	err   error
}

func (r *recordingHandoffs) RecordConversationTurn(_ context.Context, event StateEvent) error {
	r.calls++
	r.event = event
	return r.err
}

func TestHandoffDispositionAutomaticallyMarksConversationPending(t *testing.T) {
	t.Parallel()
	handoffs := &recordingHandoffs{}
	service := NewService(NewMemoryRepository(), fixedAnswerer{result: runtime.Result{TerminalDisposition: runtime.DispositionHandoff, Text: "A human will help."}}, WithStateRecorder(handoffs))
	command := SubmitQuestionCommand{WorkspaceID: "ws", AgentID: "agent", VisitorID: "visitor", Channel: ChannelWidget, RequestID: "req", IdempotencyKey: "ik", Question: "help"}
	if _, err := service.SubmitQuestion(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if handoffs.calls != 1 || handoffs.event.Disposition != runtime.DispositionHandoff || handoffs.event.VisitorID != "visitor" {
		t.Fatalf("handoff = %#v", handoffs)
	}
	if _, err := service.SubmitQuestion(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if handoffs.calls != 2 {
		t.Fatalf("replay should retry idempotent transition; calls = %d", handoffs.calls)
	}
}

func TestStateTransitionFailureIsRetriedOnIdempotentReplay(t *testing.T) {
	t.Parallel()
	recorder := &recordingHandoffs{err: errors.New("state unavailable")}
	service := NewService(NewMemoryRepository(), fixedAnswerer{result: runtime.Result{TerminalDisposition: runtime.DispositionHandoff}}, WithStateRecorder(recorder))
	command := SubmitQuestionCommand{WorkspaceID: "ws", AgentID: "agent", VisitorID: "visitor", Channel: ChannelWidget, RequestID: "req", IdempotencyKey: "ik", Question: "help"}
	if _, err := service.SubmitQuestion(context.Background(), command); err == nil {
		t.Fatal("expected transition failure")
	}
	recorder.err = nil
	if _, err := service.SubmitQuestion(context.Background(), command); err != nil {
		t.Fatalf("replay transition: %v", err)
	}
	if recorder.calls != 2 {
		t.Fatalf("transition calls = %d, want 2", recorder.calls)
	}
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

func TestSubmitQuestionLoadsRecentHistoryAndListTurnsReturnsAll(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	answerer := &recordingAnswerer{result: runtime.Result{
		TerminalDisposition: runtime.DispositionRefused,
		Text:                "Not enough evidence",
	}}
	service := NewService(repository, answerer, WithHistoryTurnLimit(2))
	now := time.Date(2026, time.October, 6, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}

	command := SubmitQuestionCommand{
		WorkspaceID:    "ws_one",
		AgentID:        "agent_one",
		VisitorID:      "visitor_one",
		Channel:        ChannelWidget,
		Locale:         "en",
		RequestID:      "req_1",
		IdempotencyKey: "ik_1",
		Question:       "Question 1",
	}
	first, err := service.SubmitQuestion(context.Background(), command)
	if err != nil {
		t.Fatalf("submit first question: %v", err)
	}
	command.ConversationID = first.Message.ConversationID
	for turnNumber := 2; turnNumber <= 4; turnNumber++ {
		number := strconv.Itoa(turnNumber)
		command.RequestID = "req_" + number
		command.IdempotencyKey = "ik_" + number
		command.Question = "Question " + number
		if _, err := service.SubmitQuestion(context.Background(), command); err != nil {
			t.Fatalf("submit question %d: %v", turnNumber, err)
		}
	}

	lastCommand := answerer.commands[len(answerer.commands)-1]
	if len(lastCommand.History) != 2 {
		t.Fatalf("history length = %d, want 2", len(lastCommand.History))
	}
	if lastCommand.History[0].Question != "Question 2" ||
		lastCommand.History[1].Question != "Question 3" {
		t.Fatalf("history = %#v, want questions 2 and 3", lastCommand.History)
	}

	turns, err := service.ListTurns(
		context.Background(),
		"ws_one",
		first.Message.ConversationID,
		"visitor_one",
	)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) != 4 || turns[0].Message.Text != "Question 1" || turns[3].Message.Text != "Question 4" {
		t.Fatalf("turns = %#v", turns)
	}
	otherWorkspaceTurns, err := service.ListTurns(
		context.Background(),
		"ws_other",
		first.Message.ConversationID,
		"visitor_one",
	)
	if err != nil {
		t.Fatalf("list other workspace turns: %v", err)
	}
	if len(otherWorkspaceTurns) != 0 {
		t.Fatalf("other workspace turns = %#v, want empty", otherWorkspaceTurns)
	}
	otherVisitorTurns, err := service.ListTurns(
		context.Background(),
		"ws_one",
		first.Message.ConversationID,
		"visitor_other",
	)
	if err != nil {
		t.Fatalf("list other visitor turns: %v", err)
	}
	if len(otherVisitorTurns) != 0 {
		t.Fatalf("other visitor turns = %#v, want empty", otherVisitorTurns)
	}
}
