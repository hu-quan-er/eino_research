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

func TestAgentSynthesizerRewritesDuplicateLocalSourceIDs(t *testing.T) {
	synthesizer := NewAgentSynthesizer(staticChatModel{content: "not json"})

	out, err := synthesizer.Synthesize(context.Background(), SynthesisInput{
		Step: ResearchStep{ID: "step_1", Question: "What evidence exists?"},
		Results: []ResearcherResult{
			{
				Role: "background_researcher",
				Findings: []Finding{{
					Claim:     "background claim",
					SourceIDs: []string{"src_1"},
				}},
				Sources: []search.Source{{
					ID:    "src_1",
					Title: "Background",
					URL:   "https://example.com/background",
				}},
			},
			{
				Role: "evidence_researcher",
				Findings: []Finding{{
					Claim:     "evidence claim",
					SourceIDs: []string{"src_1"},
				}},
				Sources: []search.Source{{
					ID:    "src_1",
					Title: "Evidence",
					URL:   "https://example.com/evidence",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	if len(out.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(out.Sources))
	}
	firstID := out.ResearcherResults[0].Findings[0].SourceIDs[0]
	secondID := out.ResearcherResults[1].Findings[0].SourceIDs[0]
	if firstID == secondID {
		t.Fatalf("rewritten finding source IDs are both %q, want distinct IDs", firstID)
	}
	if firstID != out.ResearcherResults[0].Sources[0].ID {
		t.Fatalf("first finding source ID = %q, want researcher source ID %q", firstID, out.ResearcherResults[0].Sources[0].ID)
	}
	if secondID != out.ResearcherResults[1].Sources[0].ID {
		t.Fatalf("second finding source ID = %q, want researcher source ID %q", secondID, out.ResearcherResults[1].Sources[0].ID)
	}
}
