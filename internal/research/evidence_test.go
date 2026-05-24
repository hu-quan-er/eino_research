package research

import (
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestBuildSourceDocumentsCreatesChunksFromSnippets(t *testing.T) {
	docs := buildSourceDocuments([]search.Source{{
		ID:       "src_1",
		Title:    "Evidence Page",
		URL:      "https://example.com/evidence",
		Snippet:  "This page contains source-backed evidence.",
		Provider: "mock",
		Query:    "evidence query",
	}}, 20)

	if len(docs) != 1 {
		t.Fatalf("documents = %d, want 1", len(docs))
	}
	if docs[0].ID != "src_1_doc" || docs[0].SourceID != "src_1" {
		t.Fatalf("document IDs = %q/%q, want src_1_doc/src_1", docs[0].ID, docs[0].SourceID)
	}
	if len(docs[0].Chunks) < 2 {
		t.Fatalf("chunks = %d, want multiple chunks", len(docs[0].Chunks))
	}
	if docs[0].Chunks[0].ID != "src_1_chunk_1" {
		t.Fatalf("chunk ID = %q, want src_1_chunk_1", docs[0].Chunks[0].ID)
	}
	if docs[0].Chunks[0].Text == "" || len([]rune(docs[0].Chunks[0].Text)) > 20 {
		t.Fatalf("chunk text = %q, want non-empty and capped", docs[0].Chunks[0].Text)
	}
}

func TestBuildFetchedPageDocumentsUsesFullFetchedText(t *testing.T) {
	docs := buildFetchedPageDocuments([]FetchedPage{{
		URL:   "https://example.com/evidence",
		Title: "Fetched Evidence",
		Text:  "Fetched body contains more complete evidence than the snippet.",
	}}, []search.Source{{
		ID:      "src_1",
		Title:   "Snippet Evidence",
		URL:     "https://example.com/evidence",
		Snippet: "Short snippet.",
	}}, 200)

	if len(docs) != 1 {
		t.Fatalf("documents = %d, want 1", len(docs))
	}
	if docs[0].SourceID != "src_1" {
		t.Fatalf("source id = %q, want src_1", docs[0].SourceID)
	}
	if len(docs[0].Chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(docs[0].Chunks))
	}
	if docs[0].Chunks[0].Text != "Fetched body contains more complete evidence than the snippet." {
		t.Fatalf("chunk text = %q, want fetched body", docs[0].Chunks[0].Text)
	}
}

func TestNormalizeStepExecutionPrefersFetchedDocumentsOverSnippets(t *testing.T) {
	step := StepExecution{
		Step: ResearchStep{ID: "step_1", Question: "What evidence exists?"},
		ResearcherResults: []ResearcherResult{{
			Role: "evidence_researcher",
			Findings: []Finding{{
				Claim:     "Fetched evidence is used.",
				SourceIDs: []string{"src_1"},
			}},
			Sources: []search.Source{{
				ID:      "src_1",
				Title:   "Snippet Evidence",
				URL:     "https://example.com/evidence",
				Snippet: "Short snippet.",
			}},
		}},
		Documents: buildFetchedPageDocuments([]FetchedPage{{
			URL:   "https://example.com/evidence",
			Title: "Fetched Evidence",
			Text:  "Fetched full body evidence should become the cited chunk.",
		}}, []search.Source{{
			ID:      "src_1",
			Title:   "Snippet Evidence",
			URL:     "https://example.com/evidence",
			Snippet: "Short snippet.",
		}}, 200),
	}

	normalized := normalizeStepExecutionSources(step)

	if len(normalized.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(normalized.Documents))
	}
	if normalized.Documents[0].Chunks[0].Text != "Fetched full body evidence should become the cited chunk." {
		t.Fatalf("chunk text = %q, want fetched body", normalized.Documents[0].Chunks[0].Text)
	}
	ref := normalized.ResearcherResults[0].Findings[0].EvidenceRefs[0]
	if ref.Quote != "Fetched full body evidence should become the cited chunk." {
		t.Fatalf("quote = %q, want fetched body quote", ref.Quote)
	}
}

func TestNormalizeStepExecutionAddsEvidenceRefs(t *testing.T) {
	step := StepExecution{
		Step: ResearchStep{ID: "step_1", Question: "What evidence exists?"},
		ResearcherResults: []ResearcherResult{{
			Role: "evidence_researcher",
			Findings: []Finding{{
				Claim:     "Eino supports tool use.",
				SourceIDs: []string{"local_src"},
			}},
			Sources: []search.Source{{
				ID:      "local_src",
				Title:   "Tool evidence",
				URL:     "https://example.com/tools",
				Snippet: "Eino supports tool use through invokable tools.",
			}},
		}},
	}

	normalized := normalizeStepExecutionSources(step)

	if len(normalized.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(normalized.Documents))
	}
	finding := normalized.ResearcherResults[0].Findings[0]
	if len(finding.EvidenceRefs) != 1 {
		t.Fatalf("evidence refs = %d, want 1", len(finding.EvidenceRefs))
	}
	if finding.EvidenceRefs[0].SourceID != normalized.Sources[0].ID {
		t.Fatalf("evidence source ID = %q, want %q", finding.EvidenceRefs[0].SourceID, normalized.Sources[0].ID)
	}
	if finding.EvidenceRefs[0].ChunkID != normalized.Documents[0].Chunks[0].ID {
		t.Fatalf("evidence chunk ID = %q, want %q", finding.EvidenceRefs[0].ChunkID, normalized.Documents[0].Chunks[0].ID)
	}
	if finding.EvidenceRefs[0].Quote == "" {
		t.Fatal("evidence quote is empty")
	}
}

func TestStepExecutionToTodoExecutionPreservesDocuments(t *testing.T) {
	todo := validTodoPlan().Todos[1]
	step := StepExecution{
		Step:    todoToResearchStep(todo),
		Summary: "combined",
		ResearcherResults: []ResearcherResult{{
			Role: "evidence_researcher",
			Findings: []Finding{{
				Claim:     "source-backed claim",
				SourceIDs: []string{"src_1"},
			}},
			Sources: []search.Source{{
				ID:      "src_1",
				Title:   "Evidence",
				URL:     "https://example.com/evidence",
				Snippet: "Source-backed evidence snippet.",
			}},
		}},
	}

	execution := stepExecutionToTodoExecution(todo, step)

	if len(execution.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(execution.Documents))
	}
	if len(execution.Findings) != 1 || len(execution.Findings[0].EvidenceRefs) != 1 {
		t.Fatalf("findings = %#v, want evidence refs", execution.Findings)
	}
}
