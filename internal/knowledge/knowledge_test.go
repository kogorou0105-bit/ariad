package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSubmitTextIsIdempotentAndWorkspaceScoped(t *testing.T) {
	t.Parallel()
	service := NewService(NewMemoryRepository())
	command := SubmitTextCommand{
		WorkspaceID:    "ws_one",
		RequestID:      "req_one",
		IdempotencyKey: "ik_one",
		Title:          "Policy",
		Text:           "Refunds are available within 30 days.\nShipping takes two days.",
	}

	first, err := service.SubmitText(context.Background(), command)
	if err != nil {
		t.Fatalf("submit text: %v", err)
	}
	second, err := service.SubmitText(context.Background(), command)
	if err != nil {
		t.Fatalf("replay text: %v", err)
	}
	if first != second {
		t.Fatalf("idempotent result = %#v, want %#v", second, first)
	}

	chunks, err := service.ListChunks(context.Background(), "ws_one")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want 2", len(chunks))
	}
	otherWorkspace, err := service.ListChunks(context.Background(), "ws_two")
	if err != nil {
		t.Fatalf("list other workspace chunks: %v", err)
	}
	if len(otherWorkspace) != 0 {
		t.Fatalf("other workspace exposed %d chunks", len(otherWorkspace))
	}
}

func TestSubmitTextRejectsIdempotencyKeyWithDifferentPayload(t *testing.T) {
	t.Parallel()
	service := NewService(NewMemoryRepository())
	command := SubmitTextCommand{
		WorkspaceID:    "ws_one",
		IdempotencyKey: "ik_one",
		Title:          "Policy",
		Text:           "Original text",
	}
	if _, err := service.SubmitText(context.Background(), command); err != nil {
		t.Fatalf("submit text: %v", err)
	}
	command.Text = "Different text"
	if _, err := service.SubmitText(context.Background(), command); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestSubmitTextSplitsLongText(t *testing.T) {
	t.Parallel()
	service := NewService(NewMemoryRepository())
	result, err := service.SubmitText(context.Background(), SubmitTextCommand{
		WorkspaceID:    "ws_one",
		IdempotencyKey: "ik_long",
		Text:           strings.Repeat("a", maximumChunkRunes+1),
	})
	if err != nil {
		t.Fatalf("submit long text: %v", err)
	}
	if result.ChunkCount != 2 {
		t.Fatalf("chunk count = %d, want 2", result.ChunkCount)
	}
}
