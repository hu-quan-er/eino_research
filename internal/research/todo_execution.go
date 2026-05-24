package research

import "github.com/hu-quan-er/eino_research/internal/search"

// TodoStatus 表示单个 todo 的终态或中间态。
type TodoStatus string

const (
	// TodoPending 表示 todo 尚未被调度执行。
	TodoPending TodoStatus = "pending"
	// TodoRunning 表示 todo 正在执行；当前调度器主要输出终态，保留该值便于后续事件流。
	TodoRunning TodoStatus = "running"
	// TodoDone 表示 todo 已成功执行并产出结果。
	TodoDone TodoStatus = "done"
	// TodoFailed 表示 todo 执行器返回错误。
	TodoFailed TodoStatus = "failed"
	// TodoBlocked 表示 todo 因依赖失败、跳过或无法满足而没有执行。
	TodoBlocked TodoStatus = "blocked"
	// TodoSkipped 表示 todo 被 replanner 显式跳过。
	TodoSkipped TodoStatus = "skipped"
)

// TodoExecution 是 ResearchTodo 的执行结果。
//
// Findings 是从 ResearcherResults 聚合并补齐 evidence_refs 后的扁平列表，方便最终报告
// 直接按 todo 展示证据。
type TodoExecution struct {
	Todo              ResearchTodo       `json:"todo"`
	Status            TodoStatus         `json:"status"`
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	Summary           string             `json:"summary"`
	Findings          []Finding          `json:"findings,omitempty"`
	Gaps              []string           `json:"gaps,omitempty"`
	Sources           []search.Source    `json:"sources"`
	Documents         []SourceDocument   `json:"documents,omitempty"`
	Error             string             `json:"error,omitempty"`
}

// SectionExecution 是报告层的分组结果，按原始 plan.sections 顺序聚合对应 todo。
type SectionExecution struct {
	Section ResearchSection `json:"section"`
	Todos   []TodoExecution `json:"todos"`
	Summary string          `json:"summary"`
}
