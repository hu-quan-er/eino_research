package research

import (
	"context"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestRuleBasedEvidenceBinderBindsExplicitSourceID(t *testing.T) {
	binder := RuleBasedEvidenceBinder{}
	answer, err := binder.BindEvidence(context.Background(), EvidenceBindingInput{
		Answer: Answer{
			KeyFindings: []string{"Eino supports composable workflows [src_1]."},
		},
		Sources: []search.Source{{
			ID:      "src_1",
			Title:   "Eino docs",
			URL:     "https://example.com/eino",
			Snippet: "Eino supports composable workflows with tools and agents.",
		}},
		Documents: []SourceDocument{{
			ID:       "src_1_doc",
			SourceID: "src_1",
			Title:    "Eino docs",
			URL:      "https://example.com/eino",
			Chunks: []SourceChunk{{
				ID:       "src_1_chunk_1",
				SourceID: "src_1",
				Text:     "Eino supports composable workflows with tools and agents.",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("BindEvidence() error = %v", err)
	}
	if len(answer.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1", len(answer.Evidence))
	}
	evidence := answer.Evidence[0]
	if !evidence.Supported {
		t.Fatalf("evidence supported = false, reason=%q", evidence.Reason)
	}
	if evidence.Claim != "Eino supports composable workflows." {
		t.Fatalf("claim = %q, want citation stripped claim", evidence.Claim)
	}
	if len(evidence.EvidenceRefs) != 1 || evidence.EvidenceRefs[0].ChunkID != "src_1_chunk_1" {
		t.Fatalf("evidence refs = %#v, want explicit source chunk", evidence.EvidenceRefs)
	}
}

func TestRuleBasedEvidenceBinderMarksUnsupportedClaim(t *testing.T) {
	binder := RuleBasedEvidenceBinder{}
	answer, err := binder.BindEvidence(context.Background(), EvidenceBindingInput{
		Answer: Answer{
			KeyFindings: []string{"Eino guarantees zero production incidents."},
		},
		Documents: []SourceDocument{{
			ID:       "src_1_doc",
			SourceID: "src_1",
			Chunks: []SourceChunk{{
				ID:       "src_1_chunk_1",
				SourceID: "src_1",
				Text:     "Eino provides agent workflow primitives.",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("BindEvidence() error = %v", err)
	}
	if len(answer.Evidence) != 1 || answer.Evidence[0].Supported {
		t.Fatalf("evidence = %#v, want unsupported claim", answer.Evidence)
	}
	found := false
	for _, limitation := range answer.Limitations {
		if strings.Contains(limitation, "Unsupported final claim") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("limitations = %#v, want unsupported claim limitation", answer.Limitations)
	}
}
