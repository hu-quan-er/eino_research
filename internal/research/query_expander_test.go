package research

import (
	"strings"
	"testing"
)

func TestExpandTodoSearchQueriesAddsImplementationVariants(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]

	queries := ExpandTodoSearchQueries(todo, plan)

	for _, want := range []string{
		"Eino ADK ParallelAgent tools",
		"Eino ADK ParallelAgent tools official documentation",
		"Eino ADK ParallelAgent tools github repository examples",
		"Eino ADK ParallelAgent tools limitations risks counterexamples",
	} {
		if !containsString(queries, want) {
			t.Fatalf("queries = %#v, want %q", queries, want)
		}
	}
	if len(queries) > defaultExpandedSearchQueries {
		t.Fatalf("queries = %d, want <= %d", len(queries), defaultExpandedSearchQueries)
	}
}

func TestExpandTodoSearchQueriesAddsFreshnessAndComparisonVariants(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]
	todo.SearchQueries = []string{"Eino vs LangGraph"}
	todo.Question = "What latest tradeoffs exist between Eino and LangGraph?"

	queries := ExpandTodoSearchQueries(todo, plan)

	if !containsSubstring(queries, "latest 2026") {
		t.Fatalf("queries = %#v, want freshness query", queries)
	}
	if !containsSubstring(queries, "alternatives comparison tradeoffs") {
		t.Fatalf("queries = %#v, want comparison query", queries)
	}
}

func TestTodoToResearchStepUsesExpandedQueries(t *testing.T) {
	plan := validTodoPlan()
	step := todoToResearchStep(plan.Todos[1], plan)

	if !containsSubstring(step.SearchQueries, "official documentation") {
		t.Fatalf("step queries = %#v, want expanded official query", step.SearchQueries)
	}
	if !containsSubstring(step.SearchQueries, "github repository examples") {
		t.Fatalf("step queries = %#v, want implementation query", step.SearchQueries)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
