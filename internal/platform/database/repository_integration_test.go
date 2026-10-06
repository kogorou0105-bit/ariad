package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ariad/internal/conversation"
	"ariad/internal/knowledge"
	"ariad/internal/runtime"
	"ariad/internal/usage"

	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgresRepositories(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	database := openTestDatabase(t)
	knowledgeRepository := NewKnowledgeRepository(database)
	conversationRepository := NewConversationRepository(database)

	t.Run("isolates workspaces", func(t *testing.T) {
		testWorkspaceIsolation(t, knowledgeRepository, conversationRepository)
	})
	t.Run("replays idempotent writes", func(t *testing.T) {
		testIdempotentReplay(t, database, knowledgeRepository, conversationRepository)
	})
	t.Run("rolls back usage and outbox with turn", func(t *testing.T) {
		testTransactionRollback(t, database, conversationRepository)
	})
	t.Run("enforces visitor ownership", func(t *testing.T) {
		testVisitorOwnership(t, database, conversationRepository)
	})
	t.Run("lists recent turns in chronological order", func(t *testing.T) {
		testListConversationTurns(t, conversationRepository)
	})
}

func testListConversationTurns(t *testing.T, repository *ConversationRepository) {
	t.Helper()
	ctx := context.Background()
	baseTime := time.Date(2026, time.October, 6, 9, 0, 0, 0, time.UTC)
	for index, suffix := range []string{"history_one", "history_two", "history_three"} {
		turn, fact := testTurn(suffix, "ws_history", "conv_history", "visitor_history")
		createdAt := baseTime.Add(time.Duration(index) * time.Minute)
		turn.Message.CreatedAt = createdAt
		turn.Answer.CreatedAt = createdAt
		fact.OccurredAt = createdAt
		if _, err := repository.SaveTurn(
			ctx,
			turn.Message.WorkspaceID,
			"idem_"+suffix,
			"fingerprint_"+suffix,
			turn,
			fact,
		); err != nil {
			t.Fatalf("save turn %s: %v", suffix, err)
		}
	}

	recent, err := repository.ListTurns(ctx, "ws_history", "conv_history", "visitor_history", 2)
	if err != nil {
		t.Fatalf("list recent turns: %v", err)
	}
	if len(recent) != 2 ||
		recent[0].Message.ID != "msg_history_two" ||
		recent[1].Message.ID != "msg_history_three" {
		t.Fatalf("recent turns = %#v", recent)
	}
	all, err := repository.ListTurns(
		ctx,
		"ws_history",
		"conv_history",
		"visitor_history",
		conversation.AllTurnsLimit,
	)
	if err != nil {
		t.Fatalf("list all turns: %v", err)
	}
	if len(all) != 3 || all[0].Message.ID != "msg_history_one" {
		t.Fatalf("all turns = %#v", all)
	}
	isolated, err := repository.ListTurns(
		ctx,
		"ws_other",
		"conv_history",
		"visitor_history",
		conversation.AllTurnsLimit,
	)
	if err != nil {
		t.Fatalf("list isolated turns: %v", err)
	}
	if len(isolated) != 0 {
		t.Fatalf("isolated turns = %#v, want empty", isolated)
	}
	otherVisitor, err := repository.ListTurns(
		ctx,
		"ws_history",
		"conv_history",
		"visitor_other",
		conversation.AllTurnsLimit,
	)
	if err != nil {
		t.Fatalf("list other visitor turns: %v", err)
	}
	if len(otherVisitor) != 0 {
		t.Fatalf("other visitor turns = %#v, want empty", otherVisitor)
	}
}

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	container, err := postgrescontainer.Run(
		ctx,
		"postgres:17-alpine",
		postgrescontainer.WithDatabase("ariad_test"),
		postgrescontainer.WithUsername("ariad"),
		postgrescontainer.WithPassword("ariad"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	testcontainers.CleanupContainer(t, container)
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close PostgreSQL: %v", err)
		}
	})
	migrations, err := filepath.Abs(filepath.Join("..", "..", "..", "db", "migrations"))
	if err != nil {
		t.Fatalf("resolve migration path: %v", err)
	}
	if err := goose.UpContext(ctx, database, migrations); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return database
}

func testWorkspaceIsolation(
	t *testing.T,
	knowledgeRepository *KnowledgeRepository,
	conversationRepository *ConversationRepository,
) {
	t.Helper()
	ctx := context.Background()
	source := knowledge.Source{
		ID:          "src_isolation",
		WorkspaceID: "ws_isolation_a",
		Title:       "Isolation source",
		CreatedAt:   time.Now().UTC(),
	}
	chunks := []knowledge.Chunk{{
		ID:          "chk_isolation",
		WorkspaceID: source.WorkspaceID,
		SourceID:    source.ID,
		SourceTitle: source.Title,
		Ordinal:     0,
		Text:        "Workspace A only",
	}}
	if _, err := knowledgeRepository.SaveSource(
		ctx,
		source.WorkspaceID,
		"idem_knowledge_isolation",
		"fingerprint_knowledge_isolation",
		source,
		chunks,
	); err != nil {
		t.Fatalf("save isolated source: %v", err)
	}
	otherChunks, err := knowledgeRepository.ListChunks(ctx, "ws_isolation_b")
	if err != nil {
		t.Fatalf("list other workspace chunks: %v", err)
	}
	if len(otherChunks) != 0 {
		t.Fatalf("other workspace exposed %d chunks", len(otherChunks))
	}
	turn, fact := testTurn("isolation", "ws_isolation_a", "conv_isolation", "visitor_isolation")
	if _, err := conversationRepository.SaveTurn(
		ctx,
		turn.Message.WorkspaceID,
		"idem_turn_isolation",
		"fingerprint_turn_isolation",
		turn,
		fact,
	); err != nil {
		t.Fatalf("save isolated turn: %v", err)
	}
	if _, found, err := conversationRepository.FindTurnByIdempotencyKey(
		ctx,
		"ws_isolation_b",
		"idem_turn_isolation",
	); err != nil {
		t.Fatalf("find turn in other workspace: %v", err)
	} else if found {
		t.Fatal("other workspace found isolated turn")
	}
}

func testIdempotentReplay(
	t *testing.T,
	database *sql.DB,
	knowledgeRepository *KnowledgeRepository,
	conversationRepository *ConversationRepository,
) {
	t.Helper()
	ctx := context.Background()
	source := knowledge.Source{
		ID:          "src_replay",
		WorkspaceID: "ws_replay",
		Title:       "Replay source",
		CreatedAt:   time.Now().UTC(),
	}
	chunks := []knowledge.Chunk{{
		ID:          "chk_replay",
		WorkspaceID: source.WorkspaceID,
		SourceID:    source.ID,
		SourceTitle: source.Title,
		Ordinal:     0,
		Text:        "Replay content",
	}}
	firstSource, err := knowledgeRepository.SaveSource(
		ctx,
		source.WorkspaceID,
		"idem_knowledge_replay",
		"fingerprint_knowledge_replay",
		source,
		chunks,
	)
	if err != nil {
		t.Fatalf("save source: %v", err)
	}
	secondSource, err := knowledgeRepository.SaveSource(
		ctx,
		source.WorkspaceID,
		"idem_knowledge_replay",
		"fingerprint_knowledge_replay",
		source,
		chunks,
	)
	if err != nil {
		t.Fatalf("replay source: %v", err)
	}
	if firstSource != secondSource {
		t.Fatalf("source replay = %#v, want %#v", secondSource, firstSource)
	}

	turn, fact := testTurn("replay", "ws_replay", "conv_replay", "visitor_replay")
	firstTurn, err := conversationRepository.SaveTurn(
		ctx,
		turn.Message.WorkspaceID,
		"idem_turn_replay",
		"fingerprint_turn_replay",
		turn,
		fact,
	)
	if err != nil {
		t.Fatalf("save turn: %v", err)
	}
	secondTurn, err := conversationRepository.SaveTurn(
		ctx,
		turn.Message.WorkspaceID,
		"idem_turn_replay",
		"fingerprint_turn_replay",
		turn,
		fact,
	)
	if err != nil {
		t.Fatalf("replay turn: %v", err)
	}
	if firstTurn.Message.ID != secondTurn.Message.ID || firstTurn.Answer.ID != secondTurn.Answer.ID {
		t.Fatalf("turn replay changed identifiers: %#v then %#v", firstTurn, secondTurn)
	}
	assertCount(t, database, 1, `SELECT count(*) FROM conversation_messages
        WHERE workspace_id = $1 AND idempotency_key = $2`, turn.Message.WorkspaceID, "idem_turn_replay")
	assertCount(t, database, 1, `SELECT count(*) FROM usage_facts
        WHERE workspace_id = $1 AND answer_id = $2`, turn.Message.WorkspaceID, turn.Answer.ID)
	assertCount(t, database, 2, `SELECT count(*) FROM outbox
        WHERE workspace_id = $1 AND event_id IN ($2, $3)`,
		turn.Message.WorkspaceID, turnEventID(turn.Answer.ID), usageEventID(turn.Answer.ID))
	assertOutboxCausation(t, database, turn.Message.WorkspaceID, turn.Answer.ID, "idem_turn_replay")
}

func testTransactionRollback(
	t *testing.T,
	database *sql.DB,
	repository *ConversationRepository,
) {
	t.Helper()
	ctx := context.Background()
	turn, fact := testTurn("rollback", "ws_rollback", "conv_rollback", "visitor_rollback")
	_, err := database.ExecContext(
		ctx,
		`INSERT INTO outbox (
            workspace_id, event_id, event_type, event_version, occurred_at,
            aggregate_type, aggregate_id, request_id, causation_id, payload
        ) VALUES ($1, $2, 'test.preexisting', 1, $3, 'test', 'test', $4, $5, '{}'::jsonb)`,
		turn.Message.WorkspaceID,
		usageEventID(turn.Answer.ID),
		turn.Answer.CreatedAt,
		fact.RequestID,
		"test_cause",
	)
	if err != nil {
		t.Fatalf("seed conflicting outbox event: %v", err)
	}
	_, err = repository.SaveTurn(
		ctx,
		turn.Message.WorkspaceID,
		"idem_turn_rollback",
		"fingerprint_turn_rollback",
		turn,
		fact,
	)
	if err == nil {
		t.Fatal("save turn succeeded despite outbox event collision")
	}
	assertCount(t, database, 0, `SELECT count(*) FROM conversation_messages
        WHERE workspace_id = $1 AND message_id = $2`, turn.Message.WorkspaceID, turn.Message.ID)
	assertCount(t, database, 0, `SELECT count(*) FROM usage_facts
        WHERE workspace_id = $1 AND answer_id = $2`, turn.Message.WorkspaceID, turn.Answer.ID)
	assertCount(t, database, 0, `SELECT count(*) FROM outbox
        WHERE workspace_id = $1 AND event_id = $2`, turn.Message.WorkspaceID, turnEventID(turn.Answer.ID))
	assertCount(t, database, 1, `SELECT count(*) FROM outbox
        WHERE workspace_id = $1 AND event_id = $2`, turn.Message.WorkspaceID, usageEventID(turn.Answer.ID))
}

func testVisitorOwnership(
	t *testing.T,
	database *sql.DB,
	repository *ConversationRepository,
) {
	t.Helper()
	ctx := context.Background()
	first, firstFact := testTurn("owner", "ws_owner", "conv_owner", "visitor_owner")
	if _, err := repository.SaveTurn(
		ctx,
		first.Message.WorkspaceID,
		"idem_turn_owner",
		"fingerprint_turn_owner",
		first,
		firstFact,
	); err != nil {
		t.Fatalf("save owner turn: %v", err)
	}
	if exists, err := repository.ConversationExists(
		ctx,
		first.Message.WorkspaceID,
		first.Message.ConversationID,
		first.Message.VisitorID,
	); err != nil || !exists {
		t.Fatalf("owner lookup = %v, %v; want true, nil", exists, err)
	}
	if exists, err := repository.ConversationExists(
		ctx,
		first.Message.WorkspaceID,
		first.Message.ConversationID,
		"visitor_intruder",
	); err != nil || exists {
		t.Fatalf("intruder lookup = %v, %v; want false, nil", exists, err)
	}
	intruder, intruderFact := testTurn(
		"intruder",
		first.Message.WorkspaceID,
		first.Message.ConversationID,
		"visitor_intruder",
	)
	_, err := repository.SaveTurn(
		ctx,
		intruder.Message.WorkspaceID,
		"idem_turn_intruder",
		"fingerprint_turn_intruder",
		intruder,
		intruderFact,
	)
	if !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatalf("intruder save error = %v, want ErrConversationNotFound", err)
	}
	assertCount(t, database, 1, `SELECT count(*) FROM conversation_messages
        WHERE workspace_id = $1 AND conversation_id = $2`,
		first.Message.WorkspaceID, first.Message.ConversationID)
}

func testTurn(
	suffix string,
	workspaceID string,
	conversationID string,
	visitorID string,
) (conversation.Turn, usage.Fact) {
	now := time.Now().UTC()
	messageID := "msg_" + suffix
	answerID := "ans_" + suffix
	turn := conversation.Turn{
		Message: conversation.Message{
			ID:             messageID,
			WorkspaceID:    workspaceID,
			ConversationID: conversationID,
			VisitorID:      visitorID,
			Channel:        conversation.ChannelWidget,
			Locale:         "en",
			Text:           "Question " + suffix,
			CreatedAt:      now,
		},
		Answer: conversation.Answer{
			ID:                  answerID,
			WorkspaceID:         workspaceID,
			ConversationID:      conversationID,
			MessageID:           messageID,
			AgentID:             "agent_test",
			TerminalDisposition: runtime.DispositionRefused,
			Text:                "Not enough evidence",
			Citations:           []conversation.Citation{},
			CreatedAt:           now,
		},
	}
	fact := usage.Fact{
		DeduplicationKey: "answer:" + answerID,
		WorkspaceID:      workspaceID,
		AgentID:          turn.Answer.AgentID,
		RequestID:        "req_" + suffix,
		AnswerID:         answerID,
		Provider:         "stub",
		Model:            "stub-grounded-answer",
		InputUnits:       1,
		CachedInputUnits: 0,
		OutputUnits:      2,
		Status:           usage.StatusCompleted,
		OccurredAt:       now,
	}
	return turn, fact
}

func assertCount(t *testing.T, database *sql.DB, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

func assertOutboxCausation(
	t *testing.T,
	database *sql.DB,
	workspaceID string,
	answerID string,
	idempotencyKey string,
) {
	t.Helper()
	ctx := context.Background()
	var turnCause string
	if err := database.QueryRowContext(
		ctx,
		`SELECT causation_id FROM outbox WHERE workspace_id = $1 AND event_id = $2`,
		workspaceID,
		turnEventID(answerID),
	).Scan(&turnCause); err != nil {
		t.Fatalf("query turn causation: %v", err)
	}
	if turnCause != idempotencyKey {
		t.Fatalf("turn causation = %q, want %q", turnCause, idempotencyKey)
	}
	var usageCause string
	if err := database.QueryRowContext(
		ctx,
		`SELECT causation_id FROM outbox WHERE workspace_id = $1 AND event_id = $2`,
		workspaceID,
		usageEventID(answerID),
	).Scan(&usageCause); err != nil {
		t.Fatalf("query usage causation: %v", err)
	}
	if usageCause != turnEventID(answerID) {
		t.Fatalf("usage causation = %q, want %q", usageCause, turnEventID(answerID))
	}
}
