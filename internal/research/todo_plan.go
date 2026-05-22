package research

import (
	"fmt"
	"strings"
)

type ResearchTodoPlan struct {
	Objective string            `json:"objective"`
	Sections  []ResearchSection `json:"sections"`
	Todos     []ResearchTodo    `json:"todos"`
}

type ResearchSection struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type ResearchTodo struct {
	ID                 string   `json:"id"`
	SectionID          string   `json:"section_id"`
	Title              string   `json:"title"`
	Question           string   `json:"question"`
	SearchQueries      []string `json:"search_queries,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	DependsOn          []string `json:"depends_on,omitempty"`
}

type ResearchTodoPlanPatch struct {
	AddTodos    []ResearchTodo  `json:"add_todos,omitempty"`
	SkipTodos   []string        `json:"skip_todos,omitempty"`
	UpdateDeps  []TodoDepsPatch `json:"update_deps,omitempty"`
	Explanation string          `json:"explanation,omitempty"`
}

type TodoDepsPatch struct {
	TodoID    string   `json:"todo_id"`
	DependsOn []string `json:"depends_on"`
}

func (p ResearchTodoPlan) Validate() error {
	if strings.TrimSpace(p.Objective) == "" {
		return fmt.Errorf("research todo plan objective is required")
	}
	if len(p.Sections) == 0 {
		return fmt.Errorf("research todo plan sections is required")
	}
	if len(p.Todos) == 0 {
		return fmt.Errorf("research todo plan todos is required")
	}

	sectionIDs := make(map[string]struct{}, len(p.Sections))
	for i, section := range p.Sections {
		id := strings.TrimSpace(section.ID)
		if id == "" {
			return fmt.Errorf("research todo plan section %d id is required", i)
		}
		if _, ok := sectionIDs[id]; ok {
			return fmt.Errorf("research todo plan section id %q is duplicated", id)
		}
		sectionIDs[id] = struct{}{}
	}

	todoIDs := make(map[string]struct{}, len(p.Todos))
	todoDeps := make(map[string][]string, len(p.Todos))
	for i, todo := range p.Todos {
		id := strings.TrimSpace(todo.ID)
		if id == "" {
			return fmt.Errorf("research todo plan todo %d id is required", i)
		}
		if _, ok := todoIDs[id]; ok {
			return fmt.Errorf("research todo plan todo id %q is duplicated", id)
		}
		todoIDs[id] = struct{}{}

		sectionID := strings.TrimSpace(todo.SectionID)
		if sectionID == "" {
			return fmt.Errorf("research todo plan todo %s section_id is required", id)
		}
		if _, ok := sectionIDs[sectionID]; !ok {
			return fmt.Errorf("research todo plan todo %s references unknown section_id %q", id, sectionID)
		}
		if strings.TrimSpace(todo.Title) == "" {
			return fmt.Errorf("research todo plan todo %s title is required", id)
		}
		if strings.TrimSpace(todo.Question) == "" {
			return fmt.Errorf("research todo plan todo %s question is required", id)
		}
		if !hasNonEmptyString(todo.AcceptanceCriteria) {
			return fmt.Errorf("research todo plan todo %s acceptance_criteria is required", id)
		}

		deps := make([]string, 0, len(todo.DependsOn))
		for _, dep := range todo.DependsOn {
			deps = append(deps, strings.TrimSpace(dep))
		}
		todoDeps[id] = deps
	}

	for todoID, deps := range todoDeps {
		for _, dep := range deps {
			if _, ok := todoIDs[dep]; !ok {
				return fmt.Errorf("research todo plan todo %s depends on unknown todo %q", todoID, dep)
			}
		}
	}

	if err := validateTodoPlanAcyclic(todoDeps); err != nil {
		return err
	}
	return nil
}

func validateTodoPlanAcyclic(deps map[string][]string) error {
	const (
		visiting = 1
		visited  = 2
	)
	state := make(map[string]int, len(deps))

	var visit func(string) error
	visit = func(todoID string) error {
		switch state[todoID] {
		case visiting:
			return fmt.Errorf("research todo plan dependency cycle includes todo %s", todoID)
		case visited:
			return nil
		}

		state[todoID] = visiting
		for _, dep := range deps[todoID] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[todoID] = visited
		return nil
	}

	for todoID := range deps {
		if err := visit(todoID); err != nil {
			return err
		}
	}
	return nil
}

func applyResearchTodoPlanPatch(plan ResearchTodoPlan, patch ResearchTodoPlanPatch, completed map[string]TodoExecution) (ResearchTodoPlan, error) {
	completedIDs := make(map[string]struct{}, len(completed))
	for todoID := range completed {
		completedIDs[todoID] = struct{}{}
	}

	existingTodoIDs := make(map[string]struct{}, len(plan.Todos)+len(patch.AddTodos))
	for _, todo := range plan.Todos {
		existingTodoIDs[strings.TrimSpace(todo.ID)] = struct{}{}
	}

	for _, todo := range patch.AddTodos {
		id := strings.TrimSpace(todo.ID)
		if _, ok := existingTodoIDs[id]; ok {
			return ResearchTodoPlan{}, fmt.Errorf("todo plan patch add_todos reuses todo id %q", id)
		}
		existingTodoIDs[id] = struct{}{}
		plan.Todos = append(plan.Todos, todo)
	}

	if len(patch.SkipTodos) > 0 && strings.TrimSpace(patch.Explanation) == "" {
		return ResearchTodoPlan{}, fmt.Errorf("todo plan patch skip_todos requires explanation")
	}
	skipped := make(map[string]struct{}, len(patch.SkipTodos))
	for _, todoID := range patch.SkipTodos {
		id := strings.TrimSpace(todoID)
		if _, ok := completedIDs[id]; ok {
			return ResearchTodoPlan{}, fmt.Errorf("todo plan patch cannot skip completed todo %q", id)
		}
		skipped[id] = struct{}{}
	}

	for _, depPatch := range patch.UpdateDeps {
		id := strings.TrimSpace(depPatch.TodoID)
		if _, ok := completedIDs[id]; ok {
			return ResearchTodoPlan{}, fmt.Errorf("todo plan patch cannot update completed todo %q", id)
		}
		updated := false
		for i := range plan.Todos {
			if strings.TrimSpace(plan.Todos[i].ID) != id {
				continue
			}
			plan.Todos[i].DependsOn = depPatch.DependsOn
			updated = true
			break
		}
		if !updated {
			return ResearchTodoPlan{}, fmt.Errorf("todo plan patch updates unknown todo %q", id)
		}
	}

	if len(skipped) > 0 {
		todos := make([]ResearchTodo, 0, len(plan.Todos)-len(skipped))
		for _, todo := range plan.Todos {
			if _, ok := skipped[strings.TrimSpace(todo.ID)]; ok {
				continue
			}
			todos = append(todos, todo)
		}
		plan.Todos = todos
	}

	if err := plan.Validate(); err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("invalid patched todo plan: %w", err)
	}
	return plan, nil
}
