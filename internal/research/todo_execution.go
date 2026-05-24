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
	// Todo 是本次执行对应的原始任务定义。
	Todo ResearchTodo `json:"todo"`
	// Status 是调度器记录的 todo 执行状态。
	Status TodoStatus `json:"status"`
	// ResearcherResults 是该 todo 内部所有 researcher 的原始输出。
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	// Summary 是 synthesizer 对 todo 的综合摘要。
	Summary string `json:"summary"`
	// Findings 是聚合后的扁平 finding 列表，报告层优先读取它。
	Findings []Finding `json:"findings,omitempty"`
	// Gaps 记录 todo 仍未解决的问题或证据缺口。
	Gaps []string `json:"gaps,omitempty"`
	// Sources 是该 todo 使用或发现的来源。
	Sources []search.Source `json:"sources"`
	// Documents 是该 todo 生成的可引用正文切片。
	Documents []SourceDocument `json:"documents,omitempty"`
	// Error 是执行失败或 blocked/skipped 的可读原因。
	Error string `json:"error,omitempty"`
}

// SectionExecution 是报告层的分组结果，按原始 plan.sections 顺序聚合对应 todo。
type SectionExecution struct {
	// Section 是对应的 plan section。
	Section ResearchSection `json:"section"`
	// Todos 是该 section 下的 todo 执行结果。
	Todos []TodoExecution `json:"todos"`
	// Summary 是该 section 下 todo summaries 的合并文本。
	Summary string `json:"summary"`
}
