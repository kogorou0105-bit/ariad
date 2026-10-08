// Package playground owns stateless answer debugging without conversation persistence.
package playground

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ariad/internal/agent"
	"ariad/internal/knowledge"
	"ariad/internal/modelconfig"
	"ariad/internal/retrieval"
	"ariad/internal/runtime"
)

const (
	MinimumTopK = 1
	MaximumTopK = 20
)

var (
	ErrInvalidQuestion    = errors.New("question is required")
	ErrInvalidTopK        = errors.New("top_k must be between 1 and 20")
	ErrInvalidThreshold   = errors.New("threshold must be between 0 and 1")
	ErrModelNotConfigured = errors.New("model API key is not configured")
	ErrNoKnowledge        = errors.New("no knowledge sources are ready for retrieval")
)

type Command struct {
	WorkspaceID, AgentID, Question, Locale string
	TopK                                   int
	Threshold                              float64
}
type Hit struct {
	EvidenceID, SourceID, ChunkID, SourceTitle, Text string
	Score                                            float64
}
type Result struct {
	Answer, Outcome                            string
	Hits                                       []Hit
	HitCount                                   int
	Duration, RetrievalDuration, ModelDuration time.Duration
}

type Service struct {
	agents    agent.Reader
	chunks    knowledge.ChunkReader
	retriever retrieval.Retriever
	model     runtime.Model
	configs   *modelconfig.Service
	clock     func() time.Time
}

func NewService(agents agent.Reader, chunks knowledge.ChunkReader, retriever retrieval.Retriever, model runtime.Model, configs *modelconfig.Service) *Service {
	return &Service{agents: agents, chunks: chunks, retriever: retriever, model: model, configs: configs, clock: time.Now}
}

func (s *Service) Ask(ctx context.Context, command Command) (Result, error) {
	if strings.TrimSpace(command.Question) == "" {
		return Result{}, ErrInvalidQuestion
	}
	if command.TopK < MinimumTopK || command.TopK > MaximumTopK {
		return Result{}, ErrInvalidTopK
	}
	if command.Threshold < 0 || command.Threshold > 1 {
		return Result{}, ErrInvalidThreshold
	}
	status, err := s.configs.GetStatus(ctx, command.WorkspaceID)
	if err != nil {
		return Result{}, fmt.Errorf("resolve model configuration: %w", err)
	}
	if status.APIKeyMask == "" {
		return Result{}, ErrModelNotConfigured
	}
	chunks, err := s.chunks.ListChunks(ctx, command.WorkspaceID)
	if err != nil {
		return Result{}, fmt.Errorf("list knowledge: %w", err)
	}
	if len(chunks) == 0 {
		return Result{}, ErrNoKnowledge
	}
	published, err := s.agents.GetPublished(ctx, command.WorkspaceID, command.AgentID)
	if err != nil {
		return Result{}, fmt.Errorf("resolve agent: %w", err)
	}
	started := s.clock()
	retrievalStarted := s.clock()
	evidence, err := s.retriever.Retrieve(ctx, retrieval.Query{WorkspaceID: command.WorkspaceID, Question: command.Question, Limit: command.TopK, MinimumScore: &command.Threshold})
	if err != nil {
		return Result{}, fmt.Errorf("retrieve evidence: %w", err)
	}
	retrievalDuration := s.clock().Sub(retrievalStarted)
	hits := make([]Hit, 0, len(evidence))
	modelEvidence := make([]runtime.ModelEvidence, 0, len(evidence))
	for _, item := range evidence {
		hits = append(hits, Hit{EvidenceID: item.ID, SourceID: item.SourceID, ChunkID: item.ChunkID, SourceTitle: item.SourceTitle, Text: item.Text, Score: item.Score})
		modelEvidence = append(modelEvidence, runtime.ModelEvidence{EvidenceID: item.ID, Text: item.Text})
	}
	if len(hits) == 0 {
		return Result{Outcome: "no_matches", Hits: hits, Duration: s.clock().Sub(started), RetrievalDuration: retrievalDuration}, nil
	}
	modelStarted := s.clock()
	generated, err := s.model.Generate(ctx, runtime.ModelRequest{WorkspaceID: command.WorkspaceID, AgentID: command.AgentID, Instructions: published.Instructions, Question: command.Question, Locale: command.Locale, Evidence: modelEvidence})
	if err != nil {
		return Result{}, fmt.Errorf("generate answer: %w", err)
	}
	modelDuration := s.clock().Sub(modelStarted)
	return Result{Answer: generated.Text, Outcome: "answered", Hits: hits, HitCount: len(hits), Duration: s.clock().Sub(started), RetrievalDuration: retrievalDuration, ModelDuration: modelDuration}, nil
}
