package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"ariad/internal/conversation"
	"ariad/internal/runtime"
	"ariad/internal/usage"
)

func TestHandoffReplyAndResolveLifecycleIsWorkspaceAndVisitorScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	conversations := conversation.NewMemoryRepository()
	now := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	turn := conversation.Turn{Message: conversation.Message{ID: "msg", WorkspaceID: "ws", ConversationID: "conv", VisitorID: "visitor", Channel: conversation.ChannelWidget, Text: "help", CreatedAt: now}, Answer: conversation.Answer{ID: "answer", WorkspaceID: "ws", ConversationID: "conv", MessageID: "msg", AgentID: "agent", TerminalDisposition: runtime.DispositionRefused, Text: "cannot", CreatedAt: now}}
	if _, err := conversations.SaveTurn(ctx, "ws", "ik", "fp", turn, usage.Fact{WorkspaceID: "ws"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(NewMemoryRepository(conversations))
	service.clock = func() time.Time { now = now.Add(time.Minute); return now }

	if _, err := service.Request(ctx, "other", "conv", "visitor", "visitor_requested", "visitor"); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("cross workspace error = %v", err)
	}
	state, err := service.Request(ctx, "ws", "conv", "visitor", "visitor_requested", "visitor")
	if err != nil || state.Status != StatusPending || state.RequestedBy != "visitor" {
		t.Fatalf("state = %#v, err = %v", state, err)
	}
	items, err := service.ListPending(ctx, "ws")
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, err = %v", items, err)
	}
	reply, err := service.Reply(ctx, "ws", "conv", "visitor", "admin", "Human answer")
	if err != nil || reply.AuthorID != "admin" {
		t.Fatalf("reply = %#v, err = %v", reply, err)
	}
	if err := service.Resolve(ctx, "ws", "conv", "visitor", "admin"); err != nil {
		t.Fatal(err)
	}
	items, _ = service.ListPending(ctx, "ws")
	if len(items) != 0 {
		t.Fatalf("resolved conversation remains queued: %#v", items)
	}
	state, replies, err := service.Get(ctx, "ws", "conv", "visitor")
	if err != nil || state.Status != StatusResolved || len(replies) != 1 {
		t.Fatalf("state = %#v replies = %#v err = %v", state, replies, err)
	}
	if err := service.RecordConversationTurn(ctx, conversation.StateEvent{WorkspaceID: "ws", ConversationID: "conv", VisitorID: "visitor", Disposition: runtime.DispositionAnswered, OccurredAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	state, _, err = service.Get(ctx, "ws", "conv", "visitor")
	if err != nil || state.Status != StatusOngoing {
		t.Fatalf("reopened state = %#v err = %v", state, err)
	}
}
