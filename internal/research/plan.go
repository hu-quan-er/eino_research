package research

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ResearchPlan struct {
	Steps []ResearchStep `json:"steps"`
}

type ResearchStep struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Question        string   `json:"question"`
	SearchQueries   []string `json:"search_queries"`
	ResearchAxes    []string `json:"research_axes,omitempty"`
	SuccessCriteria []string `json:"success_criteria"`
}

func (p *ResearchPlan) FirstStep() string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	b, err := json.Marshal(p.Steps[0])
	if err != nil {
		return ""
	}
	return string(b)
}

func (p *ResearchPlan) MarshalJSON() ([]byte, error) {
	type alias ResearchPlan
	return json.Marshal((*alias)(p))
}

func (p *ResearchPlan) UnmarshalJSON(b []byte) error {
	type alias ResearchPlan
	return json.Unmarshal(b, (*alias)(p))
}

func (p ResearchPlan) Validate() error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("research plan must include at least one step")
	}
	seenIDs := make(map[string]struct{}, len(p.Steps))
	for i, step := range p.Steps {
		id := strings.TrimSpace(step.ID)
		if id == "" {
			return fmt.Errorf("research plan step %d id is required", i)
		}
		if _, ok := seenIDs[id]; ok {
			return fmt.Errorf("research plan step id %q is duplicated", id)
		}
		seenIDs[id] = struct{}{}
		if strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("research plan step %s title is required", id)
		}
		if strings.TrimSpace(step.Question) == "" {
			return fmt.Errorf("research plan step %s question is required", id)
		}
		if !hasNonEmptyString(step.SearchQueries) {
			return fmt.Errorf("research plan step %s search_queries is required", id)
		}
		if !hasNonEmptyString(step.SuccessCriteria) {
			return fmt.Errorf("research plan step %s success_criteria is required", id)
		}
	}
	return nil
}

func hasNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
