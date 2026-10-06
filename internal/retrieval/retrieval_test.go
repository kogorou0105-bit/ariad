package retrieval

import (
	"context"
	"testing"

	"ariad/internal/knowledge"
)

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
