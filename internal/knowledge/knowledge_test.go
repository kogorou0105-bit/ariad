package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fixedKnowledgeEmbedder struct{ fail bool }

func (e *fixedKnowledgeEmbedder) Embed(context.Context, string, string) ([]float64, error) {
	if e.fail {
		return nil, errors.New("embedding unavailable")
	}
	return []float64{0.25, 0.75}, nil
}

func TestBackfillEmbeddingsRetriesMissingChunks(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	_, err := service.SubmitText(context.Background(), SubmitTextCommand{WorkspaceID: "ws", IdempotencyKey: "legacy", Text: "legacy knowledge"})
	if err != nil {
		t.Fatal(err)
	}
	embedder := &fixedKnowledgeEmbedder{fail: true}
	service.embedder = embedder
	first, err := service.BackfillEmbeddings(context.Background(), "ws")
	if err != nil || first.Failed != 1 || len(first.Failures) != 1 || first.Failures[0].Reason != "embedding unavailable" {
		t.Fatalf("first = %#v err = %v", first, err)
	}
	firstStatus, found, err := repository.GetEmbeddingBackfill(context.Background(), "ws")
	if err != nil || !found || firstStatus.Status != "completed" || len(firstStatus.Failures) != 1 {
		t.Fatalf("first status = %#v found = %v err = %v", firstStatus, found, err)
	}
	embedder.fail = false
	second, err := service.BackfillEmbeddings(context.Background(), "ws")
	if err != nil || second.Completed != 1 || second.Failed != 0 {
		t.Fatalf("second = %#v err = %v", second, err)
	}
	chunks, _ := repository.ListChunks(context.Background(), "ws")
	if len(chunks[0].Embedding) != 2 {
		t.Fatalf("embedding = %#v", chunks[0].Embedding)
	}
	secondStatus, found, err := repository.GetEmbeddingBackfill(context.Background(), "ws")
	if err != nil || !found || secondStatus.Status != "completed" || secondStatus.Failed != 0 {
		t.Fatalf("second status = %#v found = %v err = %v", secondStatus, found, err)
	}
}

func TestSubmitTextPersistsWhenEmbeddingFails(t *testing.T) {
	repository := NewMemoryRepository()
	var observedChunkID string
	var observedError error
	service := NewService(
		repository,
		WithEmbedder(&fixedKnowledgeEmbedder{fail: true}),
		WithEmbeddingErrorHandler(func(_ string, chunkID string, err error) {
			observedChunkID, observedError = chunkID, err
		}),
	)
	result, err := service.SubmitText(context.Background(), SubmitTextCommand{
		WorkspaceID: "ws", IdempotencyKey: "embedding-failure", Text: "knowledge remains available",
	})
	if err != nil || result.ChunkCount != 1 {
		t.Fatalf("result = %#v err = %v", result, err)
	}
	chunks, err := repository.ListChunks(context.Background(), "ws")
	if err != nil || len(chunks) != 1 || len(chunks[0].Embedding) != 0 {
		t.Fatalf("chunks = %#v err = %v", chunks, err)
	}
	if observedChunkID != chunks[0].ID || observedError == nil || observedError.Error() != "embedding unavailable" {
		t.Fatalf("observed chunk = %q error = %v", observedChunkID, observedError)
	}
}

func TestEmbeddingBackfillCanRestartAfterStaleLease(t *testing.T) {
	repository := NewMemoryRepository()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	if err := repository.SaveEmbeddingBackfill(context.Background(), "ws", EmbeddingBackfillStatus{Status: "running", UpdatedAt: now.Add(-embeddingBackfillLease)}); err != nil {
		t.Fatal(err)
	}
	service := NewService(repository)
	service.clock = func() time.Time { return now }
	status, started, err := service.StartEmbeddingBackfill(context.Background(), "ws")
	if err != nil || !started || status.Status != "running" || !status.UpdatedAt.Equal(now) {
		t.Fatalf("status = %#v started = %v err = %v", status, started, err)
	}
}

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

func TestSubmitTextPersistsSourceURLAndIncludesItInReplayIdentity(t *testing.T) {
	t.Parallel()
	repository := NewMemoryRepository()
	service := NewService(repository)
	command := SubmitTextCommand{
		WorkspaceID:    "ws_one",
		IdempotencyKey: "ik_url",
		Title:          "Policy",
		SourceURL:      "https://example.com/policy",
		Text:           "Original text",
	}
	if _, err := service.SubmitText(context.Background(), command); err != nil {
		t.Fatalf("submit text: %v", err)
	}
	stored, found, err := service.FindSubmission(context.Background(), command.WorkspaceID, command.IdempotencyKey)
	if err != nil {
		t.Fatalf("find submission: %v", err)
	}
	if !found || stored.SourceURL != command.SourceURL || stored.SourceTitle != command.Title {
		t.Fatalf("stored submission = %#v", stored)
	}
	command.SourceURL = "https://example.com/other"
	if _, err := service.SubmitText(context.Background(), command); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed URL error = %v, want ErrIdempotencyConflict", err)
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

func TestFileSourceLifecycleAndEmbeddings(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository, WithEmbedder(&fixedKnowledgeEmbedder{}))
	result, err := service.SubmitText(context.Background(), SubmitTextCommand{WorkspaceID: "ws", IdempotencyKey: "file-1", Title: "guide.txt", Text: "Initial file knowledge", SourceType: "file", FileName: "guide.txt", MediaType: "text/plain", FileContent: []byte("Initial file knowledge")})
	if err != nil {
		t.Fatal(err)
	}
	source, found, err := service.GetSource(context.Background(), "ws", result.SourceID)
	if err != nil || !found || source.Type != "file" || source.Status != "ready" || source.ChunkCount != 1 {
		t.Fatalf("source = %#v found = %v err = %v", source, found, err)
	}
	chunks, _ := service.ListChunks(context.Background(), "ws")
	if len(chunks) != 1 || len(chunks[0].Embedding) == 0 {
		t.Fatalf("chunks = %#v", chunks)
	}
	if err := service.ReprocessSource(context.Background(), source.Source, "Replacement knowledge"); err != nil {
		t.Fatal(err)
	}
	chunks, _ = service.ListChunks(context.Background(), "ws")
	if len(chunks) != 1 || chunks[0].Text != "Replacement knowledge" || len(chunks[0].Embedding) == 0 {
		t.Fatalf("reprocessed chunks = %#v", chunks)
	}
	deleted, err := service.DeleteSource(context.Background(), "ws", result.SourceID)
	if err != nil || !deleted {
		t.Fatalf("deleted = %v err = %v", deleted, err)
	}
	chunks, _ = service.ListChunks(context.Background(), "ws")
	if len(chunks) != 0 {
		t.Fatalf("chunks after delete = %#v", chunks)
	}
}
