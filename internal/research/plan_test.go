package research

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
)

func TestResearchPlanImplementsPlan(t *testing.T) {
	var _ planexecute.Plan = (*ResearchPlan)(nil)
}

func TestResearchPlanFirstStepReturnsJSON(t *testing.T) {
	plan := &ResearchPlan{Steps: []ResearchStep{{
		ID:              "step_1",
		Title:           "Map context",
		Question:        "What is Eino?",
		SearchQueries:   []string{"Eino framework"},
		ResearchAxes:    []string{"background"},
		SuccessCriteria: []string{"explain context"},
	}}}

	first := plan.FirstStep()
	var step ResearchStep
	if err := json.Unmarshal([]byte(first), &step); err != nil {
		t.Fatalf("FirstStep returned invalid JSON: %v", err)
	}
	if step.ID != "step_1" {
		t.Fatalf("ID = %q, want step_1", step.ID)
	}
}

func TestResearchPlanEmptyFirstStep(t *testing.T) {
	plan := &ResearchPlan{}
	if got := plan.FirstStep(); got != "" {
		t.Fatalf("FirstStep = %q, want empty string", got)
	}
}

func TestResearchPlanJSONRoundTrip(t *testing.T) {
	original := &ResearchPlan{Steps: []ResearchStep{{
		ID:              "step_1",
		Title:           "Evidence",
		Question:        "What evidence exists?",
		SearchQueries:   []string{"eino evidence"},
		SuccessCriteria: []string{"find evidence"},
	}}}

	b, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var decoded ResearchPlan
	if err := decoded.UnmarshalJSON(b); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if decoded.Steps[0].Title != "Evidence" {
		t.Fatalf("Title = %q, want Evidence", decoded.Steps[0].Title)
	}
}

func TestResearchPlanValidateRejectsEmptySteps(t *testing.T) {
	var decoded ResearchPlan
	if err := decoded.UnmarshalJSON([]byte(`{"steps":[]}`)); err != nil {
		t.Fatalf("UnmarshalJSON returned error: %v", err)
	}
	if err := decoded.Validate(); err == nil {
		t.Fatal("Validate returned nil, want empty steps error")
	}
}

func TestResearchPlanValidateRejectsMissingRequiredStepFields(t *testing.T) {
	var decoded ResearchPlan
	err := decoded.UnmarshalJSON([]byte(`{"steps":[{"id":"step_1","title":"Evidence","question":"What evidence exists?","search_queries":[],"success_criteria":["find evidence"]}]}`))
	if err != nil {
		t.Fatalf("UnmarshalJSON returned error: %v", err)
	}
	if err := decoded.Validate(); err == nil {
		t.Fatal("Validate returned nil, want missing search query error")
	}
}

func TestResearchPlanUnmarshalRejectsStringSteps(t *testing.T) {
	var decoded ResearchPlan
	if err := decoded.UnmarshalJSON([]byte(`{"steps":["plain step"]}`)); err == nil {
		t.Fatal("UnmarshalJSON returned nil, want string step error")
	}
}
