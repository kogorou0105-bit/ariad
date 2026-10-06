package ingestion

import (
	"context"
	"errors"
	"testing"

	"ariad/internal/knowledge"
)

type fixedFetcher struct {
	page  FetchedPage
	err   error
	calls int
}

func (f *fixedFetcher) Fetch(context.Context, string) (FetchedPage, error) {
	f.calls++
	return f.page, f.err
}

func TestSubmitURLDelegatesExtractionAndIdempotencyToKnowledge(t *testing.T) {
	t.Parallel()
	fetcher := &fixedFetcher{page: FetchedPage{HTML: []byte(
		`<html><head><title>Returns page</title></head><body><p>Refunds last 30 days.</p></body></html>`,
	)}}
	knowledgeService := knowledge.NewService(knowledge.NewMemoryRepository())
	service := NewService(fetcher, knowledgeService)
	command := SubmitURLCommand{
		WorkspaceID:    "ws_one",
		URL:            "https://example.com/returns",
		RequestID:      "req_one",
		IdempotencyKey: "ik_one",
	}
	first, err := service.SubmitURL(context.Background(), command)
	if err != nil {
		t.Fatalf("submit URL: %v", err)
	}
	fetcher.err = errors.New("site temporarily unavailable")
	second, err := service.SubmitURL(context.Background(), command)
	if err != nil {
		t.Fatalf("replay URL: %v", err)
	}
	if first != second || first.ChunkCount != 1 {
		t.Fatalf("results = %#v then %#v", first, second)
	}
	chunks, err := knowledgeService.ListChunks(context.Background(), "ws_one")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Text != "Refunds last 30 days." ||
		chunks[0].SourceTitle != "Returns page" {
		t.Fatalf("chunks = %#v", chunks)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetcher.calls)
	}
	command.URL = "https://example.com/other"
	if _, err := service.SubmitURL(context.Background(), command); !errors.Is(err, knowledge.ErrIdempotencyConflict) {
		t.Fatalf("changed URL error = %v, want ErrIdempotencyConflict", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetch calls after conflict = %d, want 1", fetcher.calls)
	}
	command.URL = "https://example.com/returns"
	command.Title = "Different title"
	if _, err := service.SubmitURL(context.Background(), command); !errors.Is(err, knowledge.ErrIdempotencyConflict) {
		t.Fatalf("changed title error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestSubmitURLRejectsInvalidAndEmptyPages(t *testing.T) {
	t.Parallel()
	fetcher := &fixedFetcher{page: FetchedPage{HTML: []byte(`<html><body></body></html>`)}}
	service := NewService(fetcher, knowledge.NewService(knowledge.NewMemoryRepository()))
	command := SubmitURLCommand{
		WorkspaceID:    "ws_one",
		URL:            "ftp://example.com/file",
		RequestID:      "req_one",
		IdempotencyKey: "ik_one",
	}
	if _, err := service.SubmitURL(context.Background(), command); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("invalid URL error = %v, want ErrInvalidURL", err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
	}
	command.URL = "https://example.com/empty"
	if _, err := service.SubmitURL(context.Background(), command); !errors.Is(err, ErrNoContent) {
		t.Fatalf("empty page error = %v, want ErrNoContent", err)
	}
}
