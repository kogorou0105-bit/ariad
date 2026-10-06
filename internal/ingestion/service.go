package ingestion

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"ariad/internal/knowledge"
)

const maximumURLLength = 2_048

var (
	// ErrInvalidURL indicates that a URL is malformed or uses an unsupported scheme.
	ErrInvalidURL = errors.New("a valid HTTP or HTTPS URL is required")
	// ErrUnsafeURL indicates that a URL resolves to a network destination that must not be fetched.
	ErrUnsafeURL = errors.New("URL destination is not allowed")
	// ErrNoContent indicates that a fetched document has no usable body text.
	ErrNoContent = errors.New("fetched page contains no usable text")
)

// FetchedPage is the bounded document returned by a Fetcher.
type FetchedPage struct {
	URL  string
	HTML []byte
}

// Fetcher retrieves one public web page. Network policy belongs to its adapter.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (FetchedPage, error)
}

// SubmitURLCommand carries tenant and retry context for one URL ingestion.
type SubmitURLCommand struct {
	WorkspaceID    string
	URL            string
	Title          string
	RequestID      string
	IdempotencyKey string
}

// SubmitURLResult identifies the knowledge source created from a fetched page.
type SubmitURLResult struct {
	WorkspaceID string
	SourceID    string
	ChunkCount  int
}

// URLSubmitter ingests one public web page into knowledge.
type URLSubmitter interface {
	SubmitURL(ctx context.Context, command SubmitURLCommand) (SubmitURLResult, error)
}

// Service coordinates URL validation, fetching, extraction and knowledge submission.
type Service struct {
	fetcher   Fetcher
	knowledge knowledge.Submitter
}

// NewService creates a synchronous URL ingestion service.
func NewService(fetcher Fetcher, knowledgeSubmitter knowledge.Submitter) *Service {
	return &Service{fetcher: fetcher, knowledge: knowledgeSubmitter}
}

// SubmitURL fetches and extracts a page, then delegates chunking, idempotency
// and workspace-scoped persistence to knowledge.Service.
func (s *Service) SubmitURL(
	ctx context.Context,
	command SubmitURLCommand,
) (SubmitURLResult, error) {
	if command.WorkspaceID == "" || command.RequestID == "" || command.IdempotencyKey == "" {
		return SubmitURLResult{}, errors.New("workspace, request and idempotency key are required")
	}
	normalizedURL, err := validateURL(command.URL)
	if err != nil {
		return SubmitURLResult{}, err
	}
	previous, found, err := s.knowledge.FindSubmission(
		ctx,
		command.WorkspaceID,
		command.IdempotencyKey,
	)
	if err != nil {
		return SubmitURLResult{}, fmt.Errorf("find knowledge submission: %w", err)
	}
	if found {
		requestedTitle := strings.TrimSpace(command.Title)
		if previous.SourceURL != normalizedURL ||
			(requestedTitle != "" && previous.SourceTitle != requestedTitle) {
			return SubmitURLResult{}, knowledge.ErrIdempotencyConflict
		}
		return submitURLResult(previous.Result), nil
	}
	fetched, err := s.fetcher.Fetch(ctx, normalizedURL)
	if err != nil {
		return SubmitURLResult{}, fmt.Errorf("fetch page: %w", err)
	}
	extracted, err := ExtractHTML(fetched.HTML)
	if err != nil {
		return SubmitURLResult{}, fmt.Errorf("extract page: %w", err)
	}
	if extracted.Text == "" {
		return SubmitURLResult{}, ErrNoContent
	}
	title := strings.TrimSpace(command.Title)
	if title == "" {
		title = extracted.Title
	}
	if title == "" {
		title = normalizedURL
	}
	result, err := s.knowledge.SubmitText(ctx, knowledge.SubmitTextCommand{
		WorkspaceID:    command.WorkspaceID,
		RequestID:      command.RequestID,
		IdempotencyKey: command.IdempotencyKey,
		Title:          title,
		SourceURL:      normalizedURL,
		Text:           extracted.Text,
	})
	if err != nil {
		return SubmitURLResult{}, fmt.Errorf("submit fetched knowledge: %w", err)
	}
	return submitURLResult(result), nil
}

func submitURLResult(result knowledge.SubmitTextResult) SubmitURLResult {
	return SubmitURLResult{
		WorkspaceID: result.WorkspaceID,
		SourceID:    result.SourceID,
		ChunkCount:  result.ChunkCount,
	}
}

func validateURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || len(trimmed) > maximumURLLength {
		return "", ErrInvalidURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || parsed.User != nil {
		return "", ErrInvalidURL
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", ErrInvalidURL
	}
	return parsed.String(), nil
}
