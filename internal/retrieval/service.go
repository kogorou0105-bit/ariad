package retrieval

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"ariad/internal/knowledge"
)

const (
	defaultLimit             = 5
	defaultSemanticThreshold = 0.35
	lexicalWeight            = 0.35
	semanticWeight           = 0.65
)

// Query is a tenant-scoped evidence search request.
type Query struct {
	WorkspaceID  string
	Question     string
	Limit        int
	MinimumScore *float64
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
	chunks           knowledge.ChunkReader
	embedder         Embedder
	mu               sync.RWMutex
	vectors          map[string][]float64
	onEmbeddingError func(error)
	thresholds       SemanticThresholdProvider
}

// Embedder converts text to a semantic vector. Implementations may call an
// OpenAI-compatible embeddings endpoint or a local model.
type Embedder interface {
	Embed(context.Context, string, string) ([]float64, error)
}

type SemanticThresholdProvider interface {
	SemanticThreshold(context.Context, string) (float64, error)
}

type Option func(*Service)

func WithEmbedder(embedder Embedder) Option {
	return func(service *Service) { service.embedder = embedder }
}
func WithEmbeddingErrorHandler(handler func(error)) Option {
	return func(service *Service) { service.onEmbeddingError = handler }
}
func WithSemanticThresholdProvider(provider SemanticThresholdProvider) Option {
	return func(service *Service) { service.thresholds = provider }
}

// NewService creates a lexical retrieval service.
func NewService(chunks knowledge.ChunkReader, options ...Option) *Service {
	service := &Service{chunks: chunks, vectors: make(map[string][]float64)}
	for _, option := range options {
		option(service)
	}
	return service
}

// Retrieve ranks chunks by normalized token overlap.
func (s *Service) Retrieve(ctx context.Context, query Query) ([]Evidence, error) {
	chunks, err := s.chunks.ListChunks(ctx, query.WorkspaceID)
	if err != nil {
		return nil, err
	}
	queryTokens := tokenSet(query.Question)
	if len(queryTokens) == 0 && s.embedder == nil {
		return []Evidence{}, nil
	}

	queryVector := []float64(nil)
	if s.embedder != nil {
		var embeddingErr error
		queryVector, embeddingErr = s.embedder.Embed(ctx, query.WorkspaceID, query.Question)
		if embeddingErr != nil && s.onEmbeddingError != nil {
			s.onEmbeddingError(embeddingErr)
		}
	}
	threshold := defaultSemanticThreshold
	if s.thresholds != nil {
		configuredThreshold, thresholdErr := s.thresholds.SemanticThreshold(ctx, query.WorkspaceID)
		if thresholdErr != nil {
			if s.onEmbeddingError != nil {
				s.onEmbeddingError(thresholdErr)
			}
		} else if configuredThreshold > 0 && configuredThreshold <= 1 {
			threshold = configuredThreshold
		}
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
		lexical := 0.0
		if len(queryTokens) > 0 {
			lexical = float64(matches) / float64(len(queryTokens))
		}
		semanticSimilarity, semanticScore := 0.0, 0.0
		if len(queryVector) > 0 {
			vector, vectorErr := s.chunkVector(ctx, chunk)
			if vectorErr != nil && s.onEmbeddingError != nil {
				s.onEmbeddingError(vectorErr)
			}
			if vectorErr == nil {
				semanticSimilarity = cosine(queryVector, vector)
				semanticScore = normalizedCosine(semanticSimilarity)
			}
		}
		if matches == 0 && semanticSimilarity < threshold {
			continue
		}
		evidence = append(evidence, Evidence{
			ID:          "ev_" + chunk.ID,
			WorkspaceID: query.WorkspaceID,
			SourceID:    chunk.SourceID,
			ChunkID:     chunk.ID,
			SourceTitle: chunk.SourceTitle,
			Text:        chunk.Text,
			Score:       lexicalWeight*lexical + semanticWeight*semanticScore,
		})
	}
	if query.MinimumScore != nil {
		filtered := evidence[:0]
		for _, item := range evidence {
			if item.Score >= *query.MinimumScore {
				filtered = append(filtered, item)
			}
		}
		evidence = filtered
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

func normalizedCosine(similarity float64) float64 {
	return math.Max(0, math.Min(1, (similarity+1)/2))
}

func (s *Service) chunkVector(ctx context.Context, chunk knowledge.Chunk) ([]float64, error) {
	if len(chunk.Embedding) > 0 {
		return chunk.Embedding, nil
	}
	s.mu.RLock()
	vector, found := s.vectors[chunk.ID]
	s.mu.RUnlock()
	if found {
		return vector, nil
	}
	vector, err := s.embedder.Embed(ctx, chunk.WorkspaceID, chunk.Text)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.vectors[chunk.ID] = vector
	s.mu.Unlock()
	return vector, nil
}

func cosine(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	dot, leftNorm, rightNorm := 0.0, 0.0, 0.0
	for index := range left {
		dot += left[index] * right[index]
		leftNorm += left[index] * left[index]
		rightNorm += right[index] * right[index]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
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
