package conversation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"ariad/internal/runtime"
	"ariad/internal/usage"
)

// ErrConversationNotFound indicates that a supplied conversation does not
// belong to the workspace and visitor context of the command.
var (
	ErrConversationNotFound = errors.New("conversation not found")
	ErrIdempotencyConflict  = errors.New("idempotency key reused with different question payload")
)

// Channel identifies a conversation ingress adapter.
type Channel string

const (
	// ChannelWidget identifies the public widget adapter.
	ChannelWidget Channel = "widget"
)

// Message is the immutable visitor input for an answer turn.
type Message struct {
	ID             string
	WorkspaceID    string
	ConversationID string
	VisitorID      string
	Channel        Channel
	Locale         string
	Text           string
	CreatedAt      time.Time
}

// Citation is a durable server mapping from answer evidence to source text.
type Citation struct {
	ID          string
	EvidenceID  string
	SourceID    string
	ChunkID     string
	SourceTitle string
	Quote       string
}

// Answer is the immutable terminal response to a message.
type Answer struct {
	ID                  string
	WorkspaceID         string
	ConversationID      string
	MessageID           string
	AgentID             string
	TerminalDisposition runtime.TerminalDisposition
	Text                string
	Citations           []Citation
	CreatedAt           time.Time
}

// Turn groups the message and answer written by one idempotent operation.
type Turn struct {
	Message Message
	Answer  Answer
}

// TurnRecord stores a turn with the request identity used for strict replay validation.
type TurnRecord struct {
	Turn               Turn
	PayloadFingerprint string
}

// SubmitQuestionCommand carries stable interaction context from an adapter.
type SubmitQuestionCommand struct {
	WorkspaceID    string
	AgentID        string
	ConversationID string
	VisitorID      string
	Channel        Channel
	Locale         string
	RequestID      string
	IdempotencyKey string
	Question       string
}

// Repository stores conversation turns behind workspace-scoped operations.
type Repository interface {
	FindTurnByIdempotencyKey(
		ctx context.Context,
		workspaceID string,
		idempotencyKey string,
	) (TurnRecord, bool, error)
	ConversationExists(
		ctx context.Context,
		workspaceID string,
		conversationID string,
		visitorID string,
	) (bool, error)
	SaveTurn(
		ctx context.Context,
		workspaceID string,
		idempotencyKey string,
		payloadFingerprint string,
		turn Turn,
		usageFact usage.Fact,
	) (Turn, error)
}

// QuestionSubmitter records a question and its terminal answer.
type QuestionSubmitter interface {
	SubmitQuestion(ctx context.Context, command SubmitQuestionCommand) (Turn, error)
}

// Service owns the conversation write path, including usage recording.
type Service struct {
	repository Repository
	answerer   runtime.Answerer
	clock      func() time.Time
}

// NewService creates a conversation service.
func NewService(
	repository Repository,
	answerer runtime.Answerer,
) *Service {
	return &Service{
		repository: repository,
		answerer:   answerer,
		clock:      time.Now,
	}
}

// SubmitQuestion runs the answer workflow and persists an idempotent turn.
func (s *Service) SubmitQuestion(
	ctx context.Context,
	command SubmitQuestionCommand,
) (Turn, error) {
	if command.WorkspaceID == "" || command.AgentID == "" ||
		command.VisitorID == "" || command.Channel == "" ||
		command.RequestID == "" || command.IdempotencyKey == "" ||
		strings.TrimSpace(command.Question) == "" {
		return Turn{}, errors.New("question context is incomplete")
	}
	if command.Channel != ChannelWidget {
		return Turn{}, errors.New("unsupported conversation channel")
	}
	payloadFingerprint := questionFingerprint(command)

	previous, found, err := s.repository.FindTurnByIdempotencyKey(
		ctx,
		command.WorkspaceID,
		command.IdempotencyKey,
	)
	if err != nil {
		return Turn{}, fmt.Errorf("find conversation turn: %w", err)
	}
	if found {
		if previous.PayloadFingerprint != payloadFingerprint {
			return Turn{}, ErrIdempotencyConflict
		}
		return previous.Turn, nil
	}
	if command.ConversationID != "" {
		exists, existsErr := s.repository.ConversationExists(
			ctx,
			command.WorkspaceID,
			command.ConversationID,
			command.VisitorID,
		)
		if existsErr != nil {
			return Turn{}, fmt.Errorf("validate conversation: %w", existsErr)
		}
		if !exists {
			return Turn{}, ErrConversationNotFound
		}
	}

	conversationID := command.ConversationID
	if conversationID == "" {
		conversationID, err = randomID("conv")
		if err != nil {
			return Turn{}, err
		}
	}
	messageID, err := randomID("msg")
	if err != nil {
		return Turn{}, err
	}
	answerID, err := randomID("ans")
	if err != nil {
		return Turn{}, err
	}

	answered, err := s.answerer.Answer(ctx, runtime.AnswerCommand{
		WorkspaceID: command.WorkspaceID,
		AgentID:     command.AgentID,
		Question:    command.Question,
		Locale:      command.Locale,
		RequestID:   command.RequestID,
	})
	if err != nil {
		return Turn{}, fmt.Errorf("answer question: %w", err)
	}

	citations := make([]Citation, 0, len(answered.Citations))
	for _, runtimeCitation := range answered.Citations {
		citationID, citationErr := randomID("cit")
		if citationErr != nil {
			return Turn{}, citationErr
		}
		citations = append(citations, Citation{
			ID:          citationID,
			EvidenceID:  runtimeCitation.EvidenceID,
			SourceID:    runtimeCitation.SourceID,
			ChunkID:     runtimeCitation.ChunkID,
			SourceTitle: runtimeCitation.SourceTitle,
			Quote:       runtimeCitation.Quote,
		})
	}

	now := s.clock().UTC()
	turn := Turn{
		Message: Message{
			ID:             messageID,
			WorkspaceID:    command.WorkspaceID,
			ConversationID: conversationID,
			VisitorID:      command.VisitorID,
			Channel:        command.Channel,
			Locale:         command.Locale,
			Text:           strings.TrimSpace(command.Question),
			CreatedAt:      now,
		},
		Answer: Answer{
			ID:                  answerID,
			WorkspaceID:         command.WorkspaceID,
			ConversationID:      conversationID,
			MessageID:           messageID,
			AgentID:             command.AgentID,
			TerminalDisposition: answered.TerminalDisposition,
			Text:                answered.Text,
			Citations:           citations,
			CreatedAt:           now,
		},
	}

	status := usage.StatusNotInvoked
	if answered.ModelUsage.Invoked {
		status = usage.StatusCompleted
	}
	usageFact := usage.Fact{
		DeduplicationKey: "answer:" + answerID,
		WorkspaceID:      command.WorkspaceID,
		AgentID:          command.AgentID,
		RequestID:        command.RequestID,
		AnswerID:         answerID,
		Provider:         answered.ModelUsage.Provider,
		Model:            answered.ModelUsage.Model,
		InputUnits:       answered.ModelUsage.InputUnits,
		CachedInputUnits: answered.ModelUsage.CachedInputUnits,
		OutputUnits:      answered.ModelUsage.OutputUnits,
		Status:           status,
		OccurredAt:       now,
	}

	saved, err := s.repository.SaveTurn(
		ctx,
		command.WorkspaceID,
		command.IdempotencyKey,
		payloadFingerprint,
		turn,
		usageFact,
	)
	if err != nil {
		return Turn{}, fmt.Errorf("save conversation turn: %w", err)
	}
	return saved, nil
}

func randomID(prefix string) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate %s identifier: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

func questionFingerprint(command SubmitQuestionCommand) string {
	digest := sha256.New()
	parts := []string{
		command.AgentID,
		command.ConversationID,
		command.VisitorID,
		string(command.Channel),
		command.Locale,
		strings.TrimSpace(command.Question),
	}
	for _, part := range parts {
		_, _ = fmt.Fprintf(digest, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
