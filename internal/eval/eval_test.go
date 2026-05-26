package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestLoadSuiteValidatesCases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "questions.yaml")
	if err := os.WriteFile(path, []byte(`cases:
  - id: eino_fit
    question: Should we use Eino?
    required_claims:
      - composable workflows
    min_citation_coverage: 0.8
`), 0o600); err != nil {
		t.Fatalf("write suite: %v", err)
	}

	suite, err := LoadSuite(path)
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	if len(suite.Cases) != 1 || suite.Cases[0].ID != "eino_fit" {
		t.Fatalf("suite = %#v, want one case", suite)
	}
}

func TestEvaluateResultPassesRuleMetrics(t *testing.T) {
	result := research.ResearchResult{
		Answer: research.Answer{
			Markdown:    "Eino supports composable workflows.",
			KeyFindings: []string{"Eino supports composable workflows [src_1]."},
			Evidence: []research.ClaimEvidence{{
				Claim:        "Eino supports composable workflows.",
				Supported:    true,
				EvidenceRefs: []research.EvidenceRef{{SourceID: "src_1", ChunkID: "src_1_chunk_1", Quote: "Composable workflows."}},
			}},
		},
		Sources: []search.Source{{
			ID:    "src_1",
			Title: "Official Eino docs",
			URL:   "https://docs.example.com/eino",
		}},
	}
	evaluation := EvaluateResult(result, Case{
		ID:                  "eino_fit",
		Question:            "Should we use Eino?",
		RequiredClaims:      []string{"composable workflows"},
		RequiredSourceHints: []string{"docs.example.com"},
		MinCitationCoverage: 1,
		MinSourceDiversity:  1,
	})

	if !evaluation.Passed {
		t.Fatalf("evaluation failed: %#v", evaluation)
	}
	if evaluation.Metrics.CitationCoverage != 1 {
		t.Fatalf("citation coverage = %.2f, want 1", evaluation.Metrics.CitationCoverage)
	}
}

func TestEvaluateResultReportsIssues(t *testing.T) {
	evaluation := EvaluateResult(research.ResearchResult{
		Answer: research.Answer{
			Markdown: "Eino guarantees zero production incidents.",
			Evidence: []research.ClaimEvidence{{
				Claim:     "Eino guarantees zero production incidents.",
				Supported: false,
			}},
		},
	}, Case{
		ID:                  "bad_claim",
		Question:            "Should we use Eino?",
		ForbiddenClaims:     []string{"zero production incidents"},
		MinCitationCoverage: 1,
	})

	if evaluation.Passed {
		t.Fatalf("evaluation passed, want failure")
	}
	joined := strings.Join(evaluation.Issues, "\n")
	for _, want := range []string{"forbidden claim present", "citation coverage", "unsupported claims"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues = %#v, want %q", evaluation.Issues, want)
		}
	}
}
