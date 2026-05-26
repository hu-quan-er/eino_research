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

func TestMarkdownIncludesExecutionSummary(t *testing.T) {
	result := sampleResult()
	result.SectionExecutions = []research.SectionExecution{{
		Section: research.ResearchSection{
			ID:    "background",
			Title: "Background and Definitions",
		},
		Todos: []research.TodoExecution{{
			Todo: research.ResearchTodo{
				ID:    "todo_background",
				Title: "Clarify core terms",
			},
			Status: research.TodoDone,
		}},
	}, {
		Section: research.ResearchSection{
			ID:    "evidence",
			Title: "Evidence and Cases",
		},
		Todos: []research.TodoExecution{{
			Todo: research.ResearchTodo{
				ID:    "todo_evidence",
				Title: "Collect evidence",
			},
			Status: research.TodoFailed,
		}},
	}}

	out := Markdown(result)
	for _, want := range []string{
		"## Execution Summary",
		"### Background and Definitions",
		"- done todo_background: Clarify core terms",
		"### Evidence and Cases",
		"- failed todo_evidence: Collect evidence",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdownIncludesTodoFindingsAndEvidenceRefs(t *testing.T) {
	result := sampleResult()
	result.TodoExecutions = []research.TodoExecution{{
		Todo: research.ResearchTodo{
			ID:    "todo_background",
			Title: "Clarify core terms",
		},
		Status: research.TodoDone,
		Findings: []research.Finding{{
			Claim:     "Eino provides an agent framework.",
			Rationale: "It ships reusable orchestration components.",
			SourceIDs: []string{"src_1"},
			EvidenceRefs: []research.EvidenceRef{{
				SourceID: "src_1",
				ChunkID:  "src_1_chunk_1",
				Quote:    "Eino includes model orchestration and tool execution.",
			}},
		}},
	}}
	result.Documents = []research.SourceDocument{{
		ID:       "src_1_doc",
		SourceID: "src_1",
		Title:    "Eino",
		URL:      "https://example.com/eino",
		Chunks: []research.SourceChunk{{
			ID:         "src_1_chunk_1",
			DocumentID: "src_1_doc",
			SourceID:   "src_1",
			Text:       "Eino includes model orchestration and tool execution.",
		}},
	}}

	out := Markdown(result)
	for _, want := range []string{
		"## Findings and Evidence",
		"### todo_background: Clarify core terms",
		"- Eino provides an agent framework. [src_1]",
		"  - Rationale: It ships reusable orchestration components.",
		`  - Evidence: "Eino includes model orchestration and tool execution." (Source: src_1 Eino - https://example.com/eino, chunk ` + "`src_1_chunk_1`" + `)`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdownFallsBackToDocumentChunkEvidence(t *testing.T) {
	result := sampleResult()
	result.TodoExecutions = []research.TodoExecution{{
		Todo: research.ResearchTodo{
			ID:    "todo_evidence",
			Title: "Collect evidence",
		},
		Status: research.TodoDone,
		Findings: []research.Finding{{
			Claim:     "Fetched pages can provide fuller evidence.",
			SourceIDs: []string{"src_1"},
		}},
	}}
	result.Documents = []research.SourceDocument{{
		ID:       "src_1_doc",
		SourceID: "src_1",
		Title:    "Eino",
		URL:      "https://example.com/eino",
		Chunks: []research.SourceChunk{{
			ID:         "src_1_chunk_1",
			DocumentID: "src_1_doc",
			SourceID:   "src_1",
			Text:       "Fetched full page text is available for evidence rendering.",
		}},
	}}

	out := Markdown(result)
	for _, want := range []string{
		"- Fetched pages can provide fuller evidence. [src_1]",
		`  - Evidence: "Fetched full page text is available for evidence rendering." (Source: src_1 Eino - https://example.com/eino, chunk ` + "`src_1_chunk_1`" + `)`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdownIncludesAnswerEvidence(t *testing.T) {
	result := sampleResult()
	result.Answer.Evidence = []research.ClaimEvidence{{
		Claim:     "Eino supports composable workflows.",
		SourceIDs: []string{"src_1"},
		EvidenceRefs: []research.EvidenceRef{{
			SourceID: "src_1",
			ChunkID:  "src_1_chunk_1",
			Quote:    "Eino supports composable workflows with tools and agents.",
		}},
		Supported: true,
		Reason:    "matched explicit source id in final answer",
	}}
	result.Documents = []research.SourceDocument{{
		ID:       "src_1_doc",
		SourceID: "src_1",
		Title:    "Eino",
		URL:      "https://example.com/eino",
		Chunks: []research.SourceChunk{{
			ID:       "src_1_chunk_1",
			SourceID: "src_1",
			Text:     "Eino supports composable workflows with tools and agents.",
		}},
	}}

	out := Markdown(result)
	for _, want := range []string{
		"## Answer Evidence",
		"- Eino supports composable workflows. [src_1] (supported)",
		"  - Reason: matched explicit source id in final answer",
		`  - Evidence: "Eino supports composable workflows with tools and agents." (Source: src_1 Eino - https://example.com/eino, chunk ` + "`src_1_chunk_1`" + `)`,
	} {
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
