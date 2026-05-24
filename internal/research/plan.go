package research

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResearchPlan 是早期 planexecute 流程使用的 step-based plan。
//
// 新主流程已经切到 ResearchTodoPlan，但保留该结构用于兼容 Eino planexecute agent、
// 回归测试，以及后续可能的 legacy migration。
type ResearchPlan struct {
	Steps []ResearchStep `json:"steps"`
}

// ResearchStep 是 legacy executor 和 todo executor 之间共享的执行描述。
//
// todoToResearchStep 会把 ResearchTodo 转成该结构，从而复用 ParallelStepExecutor、
// AgentResearcher 和 AgentSynthesizer。
type ResearchStep struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Question        string   `json:"question"`
	SearchQueries   []string `json:"search_queries"`
	ResearchAxes    []string `json:"research_axes,omitempty"`
	SuccessCriteria []string `json:"success_criteria"`
}

// FirstStep 返回 plan 中第一个 step 的 JSON 字符串。
//
// Eino planexecute.Executor 从 session 中读取 plan 后只执行当前 first step，因此这里保持
// 与该框架的接口约定一致。
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

// Validate 校验 legacy ResearchPlan 的结构性正确性。
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

// hasNonEmptyString 判断字符串数组中是否至少存在一个非空值。
func hasNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
