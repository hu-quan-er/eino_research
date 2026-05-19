package research

import "github.com/hu-quan-er/eino_research/internal/search"

type TodoStatus string

const (
	TodoPending TodoStatus = "pending"
	TodoRunning TodoStatus = "running"
	TodoDone    TodoStatus = "done"
	TodoFailed  TodoStatus = "failed"
	TodoBlocked TodoStatus = "blocked"
	TodoSkipped TodoStatus = "skipped"
)

type TodoExecution struct {
	Todo              ResearchTodo       `json:"todo"`
	Status            TodoStatus         `json:"status"`
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	Summary           string             `json:"summary"`
	Findings          []Finding          `json:"findings,omitempty"`
	Gaps              []string           `json:"gaps,omitempty"`
	Sources           []search.Source    `json:"sources"`
	Error             string             `json:"error,omitempty"`
}

type SectionExecution struct {
	Section ResearchSection `json:"section"`
	Todos   []TodoExecution `json:"todos"`
	Summary string          `json:"summary"`
}
