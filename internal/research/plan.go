package research

import "encoding/json"

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
