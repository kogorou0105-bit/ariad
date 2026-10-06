package retrieval

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"ariad/internal/knowledge"
)

const defaultLimit = 5

// Query is a tenant-scoped evidence search request.
type Query struct {
	WorkspaceID string
	Question    string
	Limit       int
}

// Evidence is a server-created view of a relevant knowledge chunk.
type Evidence struct {
	ID          string
	WorkspaceID string
	SourceID    string
	ChunkID     string
	SourceTitle string
	Text        string
	Score       float64
}

// Retriever searches for evidence without exposing storage implementation details.
type Retriever interface {
	Retrieve(ctx context.Context, query Query) ([]Evidence, error)
}

// Service performs deterministic lexical retrieval over knowledge chunks.
type Service struct {
	chunks knowledge.ChunkReader
}

// NewService creates a lexical retrieval service.
func NewService(chunks knowledge.ChunkReader) *Service {
	return &Service{chunks: chunks}
}

// Retrieve ranks chunks by normalized token overlap.
func (s *Service) Retrieve(ctx context.Context, query Query) ([]Evidence, error) {
	chunks, err := s.chunks.ListChunks(ctx, query.WorkspaceID)
	if err != nil {
		return nil, err
	}
	queryTokens := tokenSet(query.Question)
	if len(queryTokens) == 0 {
		return []Evidence{}, nil
	}

	evidence := make([]Evidence, 0, len(chunks))
	for _, chunk := range chunks {
		chunkTokens := tokenSet(chunk.Text)
		matches := 0
		for token := range queryTokens {
			if _, ok := chunkTokens[token]; ok {
				matches++
			}
		}
		if matches == 0 {
			continue
		}
		evidence = append(evidence, Evidence{
			ID:          "ev_" + chunk.ID,
			WorkspaceID: query.WorkspaceID,
			SourceID:    chunk.SourceID,
			ChunkID:     chunk.ID,
			SourceTitle: chunk.SourceTitle,
			Text:        chunk.Text,
			Score:       float64(matches) / float64(len(queryTokens)),
		})
	}

	sort.SliceStable(evidence, func(left, right int) bool {
		return evidence[left].Score > evidence[right].Score
	})
	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if len(evidence) > limit {
		evidence = evidence[:limit]
	}
	return evidence, nil
}

func tokenSet(text string) map[string]struct{} {
	result := make(map[string]struct{})
	var word []rune
	var han []rune
	flushWord := func() {
		if len(word) >= 2 {
			token := strings.ToLower(string(word))
			result[token] = struct{}{}
			if len(token) > 3 && strings.HasSuffix(token, "s") {
				result[strings.TrimSuffix(token, "s")] = struct{}{}
			}
		}
		word = nil
	}
	flushHan := func() {
		if len(han) == 1 {
			result[string(han)] = struct{}{}
		}
		for index := 0; index+1 < len(han); index++ {
			result[string(han[index:index+2])] = struct{}{}
		}
		han = nil
	}

	for _, character := range text {
		switch {
		case unicode.Is(unicode.Han, character):
			flushWord()
			han = append(han, character)
		case unicode.IsLetter(character) || unicode.IsNumber(character):
			flushHan()
			word = append(word, unicode.ToLower(character))
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return result
}
