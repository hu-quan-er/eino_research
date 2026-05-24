package research

import (
	"fmt"
	"strings"
)

// ResearchTodoPlan 是当前主流程使用的 plan schema。
//
// Planner 只负责生成 sections、todos 和依赖关系；todo 内部要派发哪些 researcher 由
// TodoDispatcher 在代码层决定，这样可以降低 planner 输出格式复杂度。
type ResearchTodoPlan struct {
	// Objective 是从用户问题提炼出的研究目标，也是每个 todo 执行时的全局上下文。
	Objective string `json:"objective"`
	// Sections 是报告和执行摘要的章节结构。
	Sections []ResearchSection `json:"sections"`
	// Todos 是调度器会执行的工作单元列表，顺序同时用于报告展示和依赖解析。
	Todos []ResearchTodo `json:"todos"`
}

// ResearchSection 用于把 todo 组织成报告和执行摘要中的逻辑章节。
type ResearchSection struct {
	// ID 是 section 的稳定引用 ID，todo.section_id 必须指向它。
	ID string `json:"id"`
	// Title 是面向用户展示的章节标题。
	Title string `json:"title"`
	// Description 是可选章节说明，用于帮助 planner 和读者理解该章节范围。
	Description string `json:"description,omitempty"`
}

// ResearchTodo 是调度器真正执行的最小工作单元。
//
// DependsOn 只允许引用同一个 plan 中的 todo id；SearchQueries 是给 researcher 的初始
// 检索提示，不代表 researcher 只能使用这些 query。
type ResearchTodo struct {
	// ID 是 todo 的稳定引用 ID，依赖关系和执行结果都通过它关联。
	ID string `json:"id"`
	// SectionID 指向所属 ResearchSection.ID。
	SectionID string `json:"section_id"`
	// Title 是面向用户展示的简短任务名。
	Title string `json:"title"`
	// Question 是该 todo 需要回答的具体研究问题。
	Question string `json:"question"`
	// SearchQueries 是给 researcher 的初始检索建议，执行时仍允许 researcher 自行扩展 query。
	SearchQueries []string `json:"search_queries,omitempty"`
	// AcceptanceCriteria 是判断 todo 是否完成的验收标准。
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	// DependsOn 是必须先完成的 todo id 列表。
	DependsOn []string `json:"depends_on,omitempty"`
}

// ResearchTodoPlanPatch 是失败后 replanner 可以返回的最小补丁格式。
//
// 目前补丁只允许新增 todo、跳过未完成 todo、或调整未完成 todo 的依赖，避免 replanner
// 修改已完成工作的历史结果。
type ResearchTodoPlanPatch struct {
	// AddTodos 是 replanner 新增的后续 todo，不允许复用既有 todo id。
	AddTodos []ResearchTodo `json:"add_todos,omitempty"`
	// SkipTodos 是 replanner 决定跳过的未完成 todo id。
	SkipTodos []string `json:"skip_todos,omitempty"`
	// UpdateDeps 替换未完成 todo 的依赖列表。
	UpdateDeps []TodoDepsPatch `json:"update_deps,omitempty"`
	// Explanation 说明为什么需要跳过或调整 plan，跳过 todo 时必填。
	Explanation string `json:"explanation,omitempty"`
}

// TodoDepsPatch 表示对单个 todo 依赖列表的替换。
type TodoDepsPatch struct {
	// TodoID 是需要更新依赖的 todo id。
	TodoID string `json:"todo_id"`
	// DependsOn 是替换后的完整依赖列表，而不是增量 patch。
	DependsOn []string `json:"depends_on"`
}

// Validate 校验 planner 输出的结构性正确性，包括必填字段、引用关系和依赖环。
//
// 语义质量检查不放在这里，而是由 todo_plan_linter.go 负责，这样结构错误和质量错误
// 可以分别定位。
func (p ResearchTodoPlan) Validate() error {
	// 第一层先检查顶层集合是否存在，避免后续引用校验出现误导性错误。
	if strings.TrimSpace(p.Objective) == "" {
		return fmt.Errorf("research todo plan objective is required")
	}
	if len(p.Sections) == 0 {
		return fmt.Errorf("research todo plan sections is required")
	}
	if len(p.Todos) == 0 {
		return fmt.Errorf("research todo plan todos is required")
	}

	// 第二层收集 section id，后续 todo.section_id 必须引用这里的已有 id。
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

	// 第三层校验 todo 自身字段，并暂存依赖关系，依赖目标是否存在会在收集完所有 todo 后检查。
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

	// 所有 todo id 收集完成后再检查依赖引用，允许 todo 依赖列表引用后面定义的 todo。
	for todoID, deps := range todoDeps {
		for _, dep := range deps {
			if _, ok := todoIDs[dep]; !ok {
				return fmt.Errorf("research todo plan todo %s depends on unknown todo %q", todoID, dep)
			}
		}
	}

	// 最后检查依赖图是否有环；调度器要求它是 DAG。
	if err := validateTodoPlanAcyclic(todoDeps); err != nil {
		return err
	}
	return nil
}

// validateTodoPlanAcyclic 使用 DFS 检查 todo 依赖图是否存在环。
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

// applyResearchTodoPlanPatch 将 replanner 的补丁应用到当前 plan。
//
// 已完成 todo 被视为执行历史，不能被跳过或改依赖；补丁应用后会重新运行完整 plan
// 校验，避免 replanner 引入悬空依赖或依赖环。
func applyResearchTodoPlanPatch(plan ResearchTodoPlan, patch ResearchTodoPlanPatch, completed map[string]TodoExecution) (ResearchTodoPlan, error) {
	// completedIDs 是补丁的保护边界：已完成 todo 不允许被跳过或改依赖。
	completedIDs := make(map[string]struct{}, len(completed))
	for todoID := range completed {
		completedIDs[todoID] = struct{}{}
	}

	// 新增 todo 必须使用全新 ID，否则旧执行结果和新定义会产生歧义。
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

	// 跳过 todo 会改变研究覆盖范围，因此必须要求 replanner 给出说明。
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

	// update_deps 采用完整替换语义，避免“追加还是删除依赖”的歧义。
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

	// 最后物理移除 skipped todo，再对整个 patched plan 运行完整 Validate。
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
