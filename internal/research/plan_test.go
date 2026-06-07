package research

import (
	"encoding/json"
	"testing"
)

func TestResearchStepFirstStepPromptReturnsJSON(t *testing.T) {
	step := ResearchStep{
		ID:              "todo_1",
		Title:           "Collect evidence",
		Question:        "What evidence exists?",
		SearchQueries:   []string{"research evidence"},
		SuccessCriteria: []string{"Cites sources."},
	}

	prompt := step.FirstStepPrompt()
	var decoded ResearchStep
	if err := json.Unmarshal([]byte(prompt), &decoded); err != nil {
		t.Fatalf("FirstStepPrompt returned invalid JSON: %v", err)
	}
	if decoded.ID != step.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, step.ID)
	}
}
