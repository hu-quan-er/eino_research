package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
)

func sampleResult() research.ResearchResult {
	return research.ResearchResult{
		Question: "Should we use Eino?",
		Answer: research.Answer{
			Markdown:    "# 结论\n\nUse Eino for this prototype.",
			Summary:     "Use Eino for this prototype.",
			KeyFindings: []string{"Eino has ADK."},
			Limitations: []string{"No HTTP service in v1."},
		},
		Sources: []search.Source{{
			ID: "src_1", Title: "Eino", URL: "https://example.com/eino", Provider: "mock",
		}},
	}
}

func TestMarkdownIncludesAnswerAndSources(t *testing.T) {
	out := Markdown(sampleResult())
	for _, want := range []string{"Use Eino", "## Sources", "https://example.com/eino"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, out)
		}
	}
}

func TestJSONIsResearchResult(t *testing.T) {
	out, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON returned error: %v", err)
	}
	var decoded research.ResearchResult
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if decoded.Question != "Should we use Eino?" {
		t.Fatalf("Question = %q", decoded.Question)
	}
}
