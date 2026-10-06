package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	dbgen "ariad/db/generated"
	"ariad/internal/conversation"
	"ariad/internal/runtime"
	"ariad/internal/usage"
)

const (
	eventVersion             = 1
	turnRecordedEventType    = "conversation.turn_recorded"
	usageRecordedEventType   = "usage.fact_recorded"
	conversationAggregate    = "conversation"
	usageFactAggregate       = "usage_fact"
	turnEventIdentifierBase  = "evt_turn_"
	usageEventIdentifierBase = "evt_usage_"
)

// ConversationRepository persists complete conversation turns and their
// required usage and outbox facts in one PostgreSQL transaction.
type ConversationRepository struct {
	database *sql.DB
	queries  *dbgen.Queries
}

var _ conversation.Repository = (*ConversationRepository)(nil)

// NewConversationRepository creates a PostgreSQL conversation adapter.
func NewConversationRepository(database *sql.DB) *ConversationRepository {
	return &ConversationRepository{database: database, queries: dbgen.New(database)}
}

// FindTurnByIdempotencyKey finds a complete turn in one workspace.
func (r *ConversationRepository) FindTurnByIdempotencyKey(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (conversation.TurnRecord, bool, error) {
	return findConversationTurn(ctx, r.queries, workspaceID, idempotencyKey)
}

// ConversationExists verifies that a conversation belongs to a workspace and visitor.
func (r *ConversationRepository) ConversationExists(
	ctx context.Context,
	workspaceID string,
	conversationID string,
	visitorID string,
) (bool, error) {
	exists, err := r.queries.ConversationExists(ctx, dbgen.ConversationExistsParams{
		WorkspaceID:    workspaceID,
		ConversationID: conversationID,
		VisitorID:      visitorID,
	})
	if err != nil {
		return false, fmt.Errorf("query conversation ownership: %w", err)
	}
	return exists, nil
}

// ListTurns returns the most recent limited set in chronological order. A
// zero limit returns the full conversation.
func (r *ConversationRepository) ListTurns(
	ctx context.Context,
	workspaceID string,
	conversationID string,
	visitorID string,
	limit int,
) ([]conversation.Turn, error) {
	if limit < 0 {
		return nil, errors.New("turn limit cannot be negative")
	}
	databaseLimit, err := checkedInt32(limit)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries.ListConversationTurns(ctx, dbgen.ListConversationTurnsParams{
		WorkspaceID:    workspaceID,
		ConversationID: conversationID,
		VisitorID:      visitorID,
		TurnLimit:      databaseLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("query conversation turns: %w", err)
	}

	turns := make([]conversation.Turn, 0)
	for _, row := range rows {
		if len(turns) == 0 || turns[len(turns)-1].Message.ID != row.MessageID {
			turns = append(turns, conversation.Turn{
				Message: conversation.Message{
					ID:             row.MessageID,
					WorkspaceID:    row.WorkspaceID,
					ConversationID: row.ConversationID,
					VisitorID:      row.VisitorID,
					Channel:        conversation.Channel(row.Channel),
					Locale:         row.Locale,
					Text:           row.MessageText,
					CreatedAt:      row.MessageCreatedAt,
				},
				Answer: conversation.Answer{
					ID:                  row.AnswerID,
					WorkspaceID:         row.WorkspaceID,
					ConversationID:      row.ConversationID,
					MessageID:           row.MessageID,
					AgentID:             row.AgentID,
					TerminalDisposition: runtime.TerminalDisposition(row.TerminalDisposition),
					Text:                row.AnswerText,
					Citations:           []conversation.Citation{},
					CreatedAt:           row.AnswerCreatedAt,
				},
			})
		}
		if row.CitationID.Valid {
			turn := &turns[len(turns)-1]
			turn.Answer.Citations = append(turn.Answer.Citations, conversation.Citation{
				ID:          row.CitationID.String,
				EvidenceID:  row.EvidenceID.String,
				SourceID:    row.SourceID.String,
				ChunkID:     row.ChunkID.String,
				SourceTitle: row.SourceTitle.String,
				Quote:       row.Quote.String,
			})
		}
	}
	return turns, nil
}

// SaveTurn commits the turn, usage fact and two independently consumable
// outbox events atomically.
func (r *ConversationRepository) SaveTurn(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	turn conversation.Turn,
	usageFact usage.Fact,
) (conversation.Turn, error) {
	if err := validateConversationWrite(
		workspaceID,
		idempotencyKey,
		payloadFingerprint,
		turn,
		usageFact,
	); err != nil {
		return conversation.Turn{}, err
	}
	turnPayload, usagePayload, err := marshalEventPayloads(turn, usageFact)
	if err != nil {
		return conversation.Turn{}, err
	}
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return conversation.Turn{}, fmt.Errorf("begin conversation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := r.queries.WithTx(tx)
	if err := queries.LockConversationSubmission(ctx, dbgen.LockConversationSubmissionParams{
		WorkspaceID:    sql.NullString{String: workspaceID, Valid: true},
		IdempotencyKey: sql.NullString{String: idempotencyKey, Valid: true},
	}); err != nil {
		return conversation.Turn{}, fmt.Errorf("lock conversation submission: %w", err)
	}
	previous, found, err := findConversationTurn(ctx, queries, workspaceID, idempotencyKey)
	if err != nil {
		return conversation.Turn{}, err
	}
	if found {
		if previous.PayloadFingerprint != payloadFingerprint {
			return conversation.Turn{}, conversation.ErrIdempotencyConflict
		}
		return previous.Turn, nil
	}
	if err := queries.LockConversation(ctx, dbgen.LockConversationParams{
		WorkspaceID:    sql.NullString{String: workspaceID, Valid: true},
		ConversationID: sql.NullString{String: turn.Message.ConversationID, Valid: true},
	}); err != nil {
		return conversation.Turn{}, fmt.Errorf("lock conversation: %w", err)
	}
	visitorID, err := queries.GetConversationVisitor(ctx, dbgen.GetConversationVisitorParams{
		WorkspaceID:    workspaceID,
		ConversationID: turn.Message.ConversationID,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return conversation.Turn{}, fmt.Errorf("get conversation visitor: %w", err)
	}
	if err == nil && visitorID != turn.Message.VisitorID {
		return conversation.Turn{}, conversation.ErrConversationNotFound
	}
	if err := insertConversationState(ctx, queries, workspaceID, idempotencyKey, payloadFingerprint, turn); err != nil {
		return conversation.Turn{}, err
	}
	if err := queries.InsertUsageFact(ctx, usageParams(usageFact)); err != nil {
		return conversation.Turn{}, fmt.Errorf("insert usage fact: %w", err)
	}
	turnEventID := turnEventID(turn.Answer.ID)
	if err := queries.InsertOutboxEvent(ctx, dbgen.InsertOutboxEventParams{
		WorkspaceID:   workspaceID,
		EventID:       turnEventID,
		EventType:     turnRecordedEventType,
		EventVersion:  eventVersion,
		OccurredAt:    turn.Answer.CreatedAt,
		AggregateType: conversationAggregate,
		AggregateID:   turn.Message.ConversationID,
		RequestID:     usageFact.RequestID,
		CausationID:   idempotencyKey,
		Payload:       turnPayload,
	}); err != nil {
		return conversation.Turn{}, fmt.Errorf("insert turn outbox event: %w", err)
	}
	if err := queries.InsertOutboxEvent(ctx, dbgen.InsertOutboxEventParams{
		WorkspaceID:   workspaceID,
		EventID:       usageEventID(turn.Answer.ID),
		EventType:     usageRecordedEventType,
		EventVersion:  eventVersion,
		OccurredAt:    usageFact.OccurredAt,
		AggregateType: usageFactAggregate,
		AggregateID:   usageFact.DeduplicationKey,
		RequestID:     usageFact.RequestID,
		CausationID:   turnEventID,
		Payload:       usagePayload,
	}); err != nil {
		return conversation.Turn{}, fmt.Errorf("insert usage outbox event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return conversation.Turn{}, fmt.Errorf("commit conversation transaction: %w", err)
	}
	return turn, nil
}

func findConversationTurn(
	ctx context.Context,
	queries *dbgen.Queries,
	workspaceID string,
	idempotencyKey string,
) (conversation.TurnRecord, bool, error) {
	message, err := queries.GetConversationMessageByIdempotencyKey(
		ctx,
		dbgen.GetConversationMessageByIdempotencyKeyParams{
			WorkspaceID:    workspaceID,
			IdempotencyKey: idempotencyKey,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return conversation.TurnRecord{}, false, nil
	}
	if err != nil {
		return conversation.TurnRecord{}, false, fmt.Errorf("get conversation message: %w", err)
	}
	answer, err := queries.GetConversationAnswerByMessageID(
		ctx,
		dbgen.GetConversationAnswerByMessageIDParams{
			WorkspaceID: workspaceID,
			MessageID:   message.MessageID,
		},
	)
	if err != nil {
		return conversation.TurnRecord{}, false, fmt.Errorf("get conversation answer: %w", err)
	}
	citationRows, err := queries.ListConversationCitationsByAnswerID(
		ctx,
		dbgen.ListConversationCitationsByAnswerIDParams{
			WorkspaceID: workspaceID,
			AnswerID:    answer.AnswerID,
		},
	)
	if err != nil {
		return conversation.TurnRecord{}, false, fmt.Errorf("list conversation citations: %w", err)
	}
	citations := make([]conversation.Citation, 0, len(citationRows))
	for _, citation := range citationRows {
		citations = append(citations, conversation.Citation{
			ID:          citation.CitationID,
			EvidenceID:  citation.EvidenceID,
			SourceID:    citation.SourceID,
			ChunkID:     citation.ChunkID,
			SourceTitle: citation.SourceTitle,
			Quote:       citation.Quote,
		})
	}
	return conversation.TurnRecord{
		Turn: conversation.Turn{
			Message: conversation.Message{
				ID:             message.MessageID,
				WorkspaceID:    message.WorkspaceID,
				ConversationID: message.ConversationID,
				VisitorID:      message.VisitorID,
				Channel:        conversation.Channel(message.Channel),
				Locale:         message.Locale,
				Text:           message.Text,
				CreatedAt:      message.CreatedAt,
			},
			Answer: conversation.Answer{
				ID:                  answer.AnswerID,
				WorkspaceID:         answer.WorkspaceID,
				ConversationID:      answer.ConversationID,
				MessageID:           answer.MessageID,
				AgentID:             answer.AgentID,
				TerminalDisposition: runtime.TerminalDisposition(answer.TerminalDisposition),
				Text:                answer.Text,
				Citations:           citations,
				CreatedAt:           answer.CreatedAt,
			},
		},
		PayloadFingerprint: message.PayloadFingerprint,
	}, true, nil
}

func insertConversationState(
	ctx context.Context,
	queries *dbgen.Queries,
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	turn conversation.Turn,
) error {
	if err := queries.InsertConversationMessage(ctx, dbgen.InsertConversationMessageParams{
		WorkspaceID:        workspaceID,
		MessageID:          turn.Message.ID,
		ConversationID:     turn.Message.ConversationID,
		VisitorID:          turn.Message.VisitorID,
		Channel:            string(turn.Message.Channel),
		Locale:             turn.Message.Locale,
		Text:               turn.Message.Text,
		IdempotencyKey:     idempotencyKey,
		PayloadFingerprint: payloadFingerprint,
		CreatedAt:          turn.Message.CreatedAt,
	}); err != nil {
		return fmt.Errorf("insert conversation message: %w", err)
	}
	if err := queries.InsertConversationAnswer(ctx, dbgen.InsertConversationAnswerParams{
		WorkspaceID:         workspaceID,
		AnswerID:            turn.Answer.ID,
		ConversationID:      turn.Answer.ConversationID,
		MessageID:           turn.Answer.MessageID,
		AgentID:             turn.Answer.AgentID,
		TerminalDisposition: string(turn.Answer.TerminalDisposition),
		Text:                turn.Answer.Text,
		CreatedAt:           turn.Answer.CreatedAt,
	}); err != nil {
		return fmt.Errorf("insert conversation answer: %w", err)
	}
	for ordinal, citation := range turn.Answer.Citations {
		citationOrdinal, err := checkedInt32(ordinal)
		if err != nil {
			return err
		}
		if err := queries.InsertConversationCitation(ctx, dbgen.InsertConversationCitationParams{
			WorkspaceID: workspaceID,
			CitationID:  citation.ID,
			AnswerID:    turn.Answer.ID,
			EvidenceID:  citation.EvidenceID,
			SourceID:    citation.SourceID,
			ChunkID:     citation.ChunkID,
			SourceTitle: citation.SourceTitle,
			Quote:       citation.Quote,
			Ordinal:     citationOrdinal,
		}); err != nil {
			return fmt.Errorf("insert conversation citation: %w", err)
		}
	}
	return nil
}

func usageParams(fact usage.Fact) dbgen.InsertUsageFactParams {
	return dbgen.InsertUsageFactParams{
		WorkspaceID:      fact.WorkspaceID,
		DeduplicationKey: fact.DeduplicationKey,
		AgentID:          fact.AgentID,
		RequestID:        fact.RequestID,
		AnswerID:         fact.AnswerID,
		Provider:         fact.Provider,
		Model:            fact.Model,
		InputUnits:       fact.InputUnits,
		CachedInputUnits: fact.CachedInputUnits,
		OutputUnits:      fact.OutputUnits,
		Status:           string(fact.Status),
		OccurredAt:       fact.OccurredAt,
	}
}

type turnRecordedPayload struct {
	ConversationID      string                      `json:"conversation_id"`
	MessageID           string                      `json:"message_id"`
	AnswerID            string                      `json:"answer_id"`
	AgentID             string                      `json:"agent_id"`
	VisitorID           string                      `json:"visitor_id"`
	Channel             conversation.Channel        `json:"channel"`
	Locale              string                      `json:"locale"`
	TerminalDisposition runtime.TerminalDisposition `json:"terminal_disposition"`
	CitationCount       int                         `json:"citation_count"`
}

type usageRecordedPayload struct {
	DeduplicationKey string       `json:"deduplication_key"`
	AnswerID         string       `json:"answer_id"`
	AgentID          string       `json:"agent_id"`
	Provider         string       `json:"provider"`
	Model            string       `json:"model"`
	InputUnits       int64        `json:"input_units"`
	CachedInputUnits int64        `json:"cached_input_units"`
	OutputUnits      int64        `json:"output_units"`
	Status           usage.Status `json:"status"`
}

func marshalEventPayloads(
	turn conversation.Turn,
	fact usage.Fact,
) (json.RawMessage, json.RawMessage, error) {
	turnPayload, err := json.Marshal(turnRecordedPayload{
		ConversationID:      turn.Message.ConversationID,
		MessageID:           turn.Message.ID,
		AnswerID:            turn.Answer.ID,
		AgentID:             turn.Answer.AgentID,
		VisitorID:           turn.Message.VisitorID,
		Channel:             turn.Message.Channel,
		Locale:              turn.Message.Locale,
		TerminalDisposition: turn.Answer.TerminalDisposition,
		CitationCount:       len(turn.Answer.Citations),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal turn event: %w", err)
	}
	usagePayload, err := json.Marshal(usageRecordedPayload{
		DeduplicationKey: fact.DeduplicationKey,
		AnswerID:         fact.AnswerID,
		AgentID:          fact.AgentID,
		Provider:         fact.Provider,
		Model:            fact.Model,
		InputUnits:       fact.InputUnits,
		CachedInputUnits: fact.CachedInputUnits,
		OutputUnits:      fact.OutputUnits,
		Status:           fact.Status,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal usage event: %w", err)
	}
	return turnPayload, usagePayload, nil
}

func validateConversationWrite(
	workspaceID string,
	idempotencyKey string,
	payloadFingerprint string,
	turn conversation.Turn,
	fact usage.Fact,
) error {
	if workspaceID == "" || idempotencyKey == "" || payloadFingerprint == "" {
		return errors.New("conversation persistence context is incomplete")
	}
	if turn.Message.WorkspaceID != workspaceID || turn.Answer.WorkspaceID != workspaceID ||
		fact.WorkspaceID != workspaceID {
		return errors.New("conversation persistence workspace mismatch")
	}
	if turn.Message.ConversationID != turn.Answer.ConversationID ||
		turn.Message.ID != turn.Answer.MessageID || fact.AnswerID != turn.Answer.ID {
		return errors.New("conversation persistence identifiers do not match")
	}
	if fact.DeduplicationKey == "" || fact.RequestID == "" {
		return errors.New("usage persistence context is incomplete")
	}
	return nil
}

func turnEventID(answerID string) string {
	return turnEventIdentifierBase + answerID
}

func usageEventID(answerID string) string {
	return usageEventIdentifierBase + answerID
}
