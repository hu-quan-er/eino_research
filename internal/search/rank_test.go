package search

import "testing"

func TestRankSourcesPrioritizesOfficialDocs(t *testing.T) {
	sources := []Source{
		{
			ID:      "src_1",
			Title:   "Random forum thread",
			URL:     "http://forum.example.com/eino",
			Snippet: "A casual discussion about Eino.",
		},
		{
			ID:      "src_2",
			Title:   "Eino official documentation",
			URL:     "https://docs.example.com/eino",
			Snippet: "Official documentation for Eino agent workflows.",
		},
	}

	ranked := RankSources("Eino agent workflow documentation", sources)

	if ranked[0].ID != "src_2" {
		t.Fatalf("top source = %q, want src_2: %#v", ranked[0].ID, ranked)
	}
	if ranked[0].RankScore <= ranked[1].RankScore {
		t.Fatalf("rank scores = %.2f <= %.2f", ranked[0].RankScore, ranked[1].RankScore)
	}
	if ranked[0].RankReason == "" {
		t.Fatal("rank reason empty")
	}
}

func TestRankSourcesKeepsStableOrderForEqualScores(t *testing.T) {
	sources := []Source{
		{ID: "src_1", Title: "A", URL: "https://a.example.com"},
		{ID: "src_2", Title: "B", URL: "https://b.example.com"},
	}

	ranked := RankSources("unrelated", sources)

	if ranked[0].ID != "src_1" || ranked[1].ID != "src_2" {
		t.Fatalf("ranked order = %#v, want stable order", ranked)
	}
}
