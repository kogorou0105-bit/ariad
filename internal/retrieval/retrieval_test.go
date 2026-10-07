package retrieval

import (
	"context"
	"strings"
	"testing"

	"ariad/internal/knowledge"
)

type semanticTestEmbedder struct{}

func (semanticTestEmbedder) Embed(_ context.Context, _ string, text string) ([]float64, error) {
	text = strings.ToLower(text)
	if strings.Contains(text, "退款") || strings.Contains(text, "refund") || strings.Contains(text, "money back") {
		return []float64{1, 0}, nil
	}
	return []float64{0, 1}, nil
}

type lowSimilarityEmbedder struct{}

func (lowSimilarityEmbedder) Embed(_ context.Context, _ string, text string) ([]float64, error) {
	if strings.Contains(text, "question") {
		return []float64{1, 0}, nil
	}
	return []float64{0.1, 0.995}, nil
}
func (lowSimilarityEmbedder) SemanticThreshold(context.Context, string) (float64, error) {
	return 0.35, nil
}

func TestSemanticThresholdExcludesUnrelatedChunks(t *testing.T) {
	repository := knowledge.NewMemoryRepository()
	knowledgeService := knowledge.NewService(repository)
	_, err := knowledgeService.SubmitText(context.Background(), knowledge.SubmitTextCommand{WorkspaceID: "ws", IdempotencyKey: "unrelated", Text: "Completely unrelated material"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repository, WithEmbedder(lowSimilarityEmbedder{}), WithSemanticThresholdProvider(lowSimilarityEmbedder{}))
	evidence, err := service.Retrieve(context.Background(), Query{WorkspaceID: "ws", Question: "question"})
	if err != nil || len(evidence) != 0 {
		t.Fatalf("evidence = %#v err = %v", evidence, err)
	}
}

func TestNormalizedCosineMatchesLexicalScoreRange(t *testing.T) {
	for _, test := range []struct {
		input, want float64
	}{{-1, 0}, {0, 0.5}, {1, 1}, {2, 1}} {
		if got := normalizedCosine(test.input); got != test.want {
			t.Fatalf("normalizedCosine(%v) = %v, want %v", test.input, got, test.want)
		}
	}
}

func TestSemanticRetrievalMatchesParaphraseAndCrossLanguage(t *testing.T) {
	repository := knowledge.NewMemoryRepository()
	service := NewService(repository, WithEmbedder(semanticTestEmbedder{}))
	knowledgeService := knowledge.NewService(repository)
	_, err := knowledgeService.SubmitText(context.Background(), knowledge.SubmitTextCommand{WorkspaceID: "ws", IdempotencyKey: "refund", Title: "Refund", Text: "退款申请必须在购买后 30 天内提交"})
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range []string{"多久之内可以退款", "What is the refund window?", "How do I get my money back?"} {
		evidence, retrieveErr := service.Retrieve(context.Background(), Query{WorkspaceID: "ws", Question: question})
		if retrieveErr != nil || len(evidence) != 1 {
			t.Fatalf("retrieve %q = %#v, %v", question, evidence, retrieveErr)
		}
	}
}

func TestRetrieveMatchesEnglishAndChinese(t *testing.T) {
	t.Parallel()
	knowledgeService := knowledge.NewService(knowledge.NewMemoryRepository())
	_, err := knowledgeService.SubmitText(context.Background(), knowledge.SubmitTextCommand{
		WorkspaceID:    "ws_one",
		IdempotencyKey: "ik_one",
		Title:          "退款政策",
		Text:           "退款申请必须在购买后 30 天内提交。 Refunds are available within 30 days.",
	})
	if err != nil {
		t.Fatalf("submit knowledge: %v", err)
	}
	service := NewService(knowledgeService)

	for _, question := range []string{"退款期限是多少？", "What is the refund window?"} {
		evidence, retrieveErr := service.Retrieve(context.Background(), Query{
			WorkspaceID: "ws_one",
			Question:    question,
		})
		if retrieveErr != nil {
			t.Fatalf("retrieve %q: %v", question, retrieveErr)
		}
		if len(evidence) != 1 {
			t.Fatalf("evidence count for %q = %d, want 1", question, len(evidence))
		}
		if evidence[0].SourceTitle != "退款政策" {
			t.Fatalf("source title = %q", evidence[0].SourceTitle)
		}
	}

	missing, err := service.Retrieve(context.Background(), Query{
		WorkspaceID: "ws_two",
		Question:    "refund",
	})
	if err != nil {
		t.Fatalf("retrieve other workspace: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("other workspace exposed %d evidence items", len(missing))
	}
}
