package conversation

import (
	"context"
	"testing"
	"time"

	"ariad/internal/usage"
)

func TestMemoryRepositoryListConversationsAggregatesSortsAndIsolatesWorkspace(t *testing.T) {
	repository := NewMemoryRepository()
	base := time.Date(2026, time.October, 7, 9, 0, 0, 0, time.UTC)
	turns := []Turn{
		listTestTurn("one", "ws_one", "conv_old", "visitor_old", "First", base),
		listTestTurn("two", "ws_one", "conv_old", "visitor_old", "Latest old", base.Add(time.Minute)),
		listTestTurn("three", "ws_one", "conv_new", "visitor_new", "Newest", base.Add(2*time.Minute)),
		listTestTurn("hidden", "ws_two", "conv_hidden", "visitor_hidden", "Hidden", base.Add(3*time.Minute)),
	}
	for _, turn := range turns {
		if _, err := repository.SaveTurn(context.Background(), turn.Message.WorkspaceID, "idem_"+turn.Message.ID, "fingerprint", turn, usage.Fact{}); err != nil {
			t.Fatalf("save %s: %v", turn.Message.ID, err)
		}
	}

	summaries, err := repository.ListConversations(context.Background(), "ws_one")
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(summaries) != 2 || summaries[0].ConversationID != "conv_new" || summaries[1].ConversationID != "conv_old" {
		t.Fatalf("summaries = %#v", summaries)
	}
	if summaries[1].MessageCount != 2 || summaries[1].LastMessageText != "Latest old" || !summaries[1].LastActivityAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("aggregated old conversation = %#v", summaries[1])
	}
}

func listTestTurn(suffix, workspaceID, conversationID, visitorID, text string, createdAt time.Time) Turn {
	return Turn{
		Message: Message{ID: "msg_" + suffix, WorkspaceID: workspaceID, ConversationID: conversationID, VisitorID: visitorID, Text: text, CreatedAt: createdAt},
		Answer:  Answer{ID: "ans_" + suffix, WorkspaceID: workspaceID, ConversationID: conversationID, MessageID: "msg_" + suffix, CreatedAt: createdAt},
	}
}
