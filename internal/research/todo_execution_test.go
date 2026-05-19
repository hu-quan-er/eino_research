package research

import (
	"encoding/json"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestTodoStatusJSONValues(t *testing.T) {
	statuses := map[TodoStatus]string{
		TodoPending: "pending",
		TodoRunning: "running",
		TodoDone:    "done",
		TodoFailed:  "failed",
		TodoBlocked: "blocked",
		TodoSkipped: "skipped",
	}

	for status, want := range statuses {
		if string(status) != want {
			t.Fatalf("status %q = %q, want %q", status, string(status), want)
		}
	}
}

func TestTodoExecutionJSONRoundTrip(t *testing.T) {
	execution := TodoExecution{
		Todo: ResearchTodo{
			ID:                 "todo_background",
			SectionID:          "background",
			Title:              "Clarify background",
			Question:           "What context matters?",
			AcceptanceCriteria: []string{"Context is clear."},
		},
		Status: TodoDone,
		ResearcherResults: []ResearcherResult{{
			Role:  "background_researcher",
			Focus: "background",
		}},
		Summary: "Eino supports agent workflows.",
		Findings: []Finding{{
			Claim:     "Eino has ADK primitives.",
			SourceIDs: []string{"todo_background_src_1"},
		}},
		Gaps: []string{"Version-specific behavior still needs checking."},
		Sources: []search.Source{{
			ID:       "todo_background_src_1",
			Title:    "Eino",
			URL:      "https://example.com/eino",
			Provider: "mock",
		}},
	}

	b, err := json.Marshal(execution)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded TodoExecution
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Status != TodoDone {
		t.Fatalf("Status = %q, want %q", decoded.Status, TodoDone)
	}
	if decoded.Todo.ID != "todo_background" {
		t.Fatalf("Todo.ID = %q", decoded.Todo.ID)
	}
	if decoded.Findings[0].SourceIDs[0] != "todo_background_src_1" {
		t.Fatalf("finding source ID = %q", decoded.Findings[0].SourceIDs[0])
	}
}

func TestSectionExecutionJSONRoundTrip(t *testing.T) {
	section := SectionExecution{
		Section: ResearchSection{
			ID:    "background",
			Title: "Background",
		},
		Todos: []TodoExecution{{
			Todo: ResearchTodo{
				ID:        "todo_background",
				SectionID: "background",
				Title:     "Clarify background",
				Question:  "What context matters?",
			},
			Status:  TodoDone,
			Summary: "Background complete.",
		}},
		Summary: "Background section complete.",
	}

	b, err := json.Marshal(section)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded SectionExecution
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Section.ID != "background" {
		t.Fatalf("Section.ID = %q", decoded.Section.ID)
	}
	if len(decoded.Todos) != 1 {
		t.Fatalf("todos = %d, want 1", len(decoded.Todos))
	}
}

func TestResearchResultTodoFieldsJSONRoundTrip(t *testing.T) {
	result := ResearchResult{
		Question: "Should we use Eino?",
		Answer: Answer{
			Summary: "Use Eino for the prototype.",
		},
		Plan: validTodoPlan(),
		SectionExecutions: []SectionExecution{{
			Section: ResearchSection{
				ID:    "background",
				Title: "Background",
			},
			Todos: []TodoExecution{{
				Todo:   validTodoPlan().Todos[0],
				Status: TodoDone,
			}},
			Summary: "Background complete.",
		}},
		TodoExecutions: []TodoExecution{{
			Todo:   validTodoPlan().Todos[0],
			Status: TodoDone,
		}},
	}

	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ResearchResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Plan.Objective != result.Plan.Objective {
		t.Fatalf("Plan.Objective = %q, want %q", decoded.Plan.Objective, result.Plan.Objective)
	}
	if len(decoded.SectionExecutions) != 1 {
		t.Fatalf("section executions = %d, want 1", len(decoded.SectionExecutions))
	}
	if len(decoded.TodoExecutions) != 1 {
		t.Fatalf("todo executions = %d, want 1", len(decoded.TodoExecutions))
	}
	if decoded.TodoExecutions[0].Status != TodoDone {
		t.Fatalf("todo status = %q, want %q", decoded.TodoExecutions[0].Status, TodoDone)
	}
}
