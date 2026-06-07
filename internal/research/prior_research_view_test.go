package research

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestPriorResearchViewSerializationShape(t *testing.T) {
	// dependency 风格：无 documents → 该 key 必须被 omitempty 省略
	depView := priorResearchView{
		Step:    ResearchStep{ID: "todo_1", Question: "Q?"},
		Summary: "dep summary",
		Sources: []search.Source{{ID: "src_1"}},
	}
	data, err := json.Marshal(depView)
	if err != nil {
		t.Fatalf("marshal dep view: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"step"`, `"researcher_results"`, `"summary"`, `"sources"`} {
		if !strings.Contains(s, want) {
			t.Errorf("dep view missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, `"documents"`) {
		t.Errorf("dep view must omit documents when empty: %s", s)
	}

	// attempt 风格：有 documents → 该 key 必须出现
	attemptView := priorResearchView{
		Step:      ResearchStep{ID: "todo_1", Question: "Q?"},
		Summary:   "attempt summary",
		Sources:   []search.Source{{ID: "src_1"}},
		Documents: []SourceDocument{{ID: "src_1_doc", SourceID: "src_1", URL: "https://example.com"}},
	}
	data, err = json.Marshal(attemptView)
	if err != nil {
		t.Fatalf("marshal attempt view: %v", err)
	}
	if !strings.Contains(string(data), `"documents"`) {
		t.Errorf("attempt view should include documents: %s", data)
	}
}
