package research

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

type staticChatModel struct {
	content string
}

func (m staticChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.content, nil), nil
}

func (m staticChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(m.content, nil)}), nil
}

func TestAgentSynthesizerFallbackIncludesResearcherSources(t *testing.T) {
	synthesizer := NewAgentSynthesizer(staticChatModel{content: "not json"})

	out, err := synthesizer.Synthesize(context.Background(), SynthesisInput{
		Step: ResearchStep{ID: "step_1", Question: "What evidence exists?"},
		Results: []ResearcherResult{{
			Role: "evidence_researcher",
			Sources: []search.Source{{
				Title: "Evidence",
				URL:   "https://example.com/evidence",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	if len(out.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(out.Sources))
	}
	if out.Sources[0].URL != "https://example.com/evidence" {
		t.Fatalf("source URL = %q, want evidence URL", out.Sources[0].URL)
	}
}

func TestAgentSynthesizerParsedJSONIncludesResearcherSourcesWhenMissing(t *testing.T) {
	synthesizer := NewAgentSynthesizer(staticChatModel{content: `{"summary":"combined"}`})

	out, err := synthesizer.Synthesize(context.Background(), SynthesisInput{
		Step: ResearchStep{ID: "step_1", Question: "What evidence exists?"},
		Results: []ResearcherResult{{
			Role: "evidence_researcher",
			Sources: []search.Source{{
				Title: "Evidence",
				URL:   "https://example.com/evidence",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	if len(out.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(out.Sources))
	}
}
