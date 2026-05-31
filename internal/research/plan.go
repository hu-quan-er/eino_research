package research

import (
	"strings"
)

// ResearchStep 是 todo 内部执行器使用的统一研究步骤描述。
//
// todoToResearchStep 会把 ResearchTodo 转成该结构，从而复用 ParallelStepExecutor、
// AgentResearcher 和 AgentSynthesizer。
type ResearchStep struct {
	// ID 是 step 的稳定引用 ID。
	ID string `json:"id"`
	// Title 是 step 的简短标题。
	Title string `json:"title"`
	// Question 是该 step 要回答的具体问题。
	Question string `json:"question"`
	// SearchQueries 是 researcher 的初始检索 query。
	SearchQueries []string `json:"search_queries"`
	// ResearchAxes 是建议 researcher 覆盖的分析角度；当前 todo-plan 主路径主要使用
	// SearchQueries/SuccessCriteria，保留该字段是为了兼容更细粒度 step 执行器。
	ResearchAxes []string `json:"research_axes,omitempty"`
	// SuccessCriteria 是判断 step 是否完成的标准。
	SuccessCriteria []string `json:"success_criteria"`
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
