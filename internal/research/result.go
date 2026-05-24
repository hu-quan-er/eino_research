package research

import "github.com/hu-quan-er/eino_research/internal/search"

// ResearchResult 是一次 research run 的完整结构化输出。
//
// 新的 todo-plan 流程会填充 Plan、TodoExecutions、SectionExecutions、Sources 和
// Documents；LegacyPlan/ExecutedSteps 用于兼容早期 planexecute 流程和相关测试。
type ResearchResult struct {
	Question          string             `json:"question"`
	Answer            Answer             `json:"answer"`
	Plan              ResearchTodoPlan   `json:"plan"`
	SectionExecutions []SectionExecution `json:"section_executions"`
	TodoExecutions    []TodoExecution    `json:"todo_executions"`
	Sources           []search.Source    `json:"sources"`
	Documents         []SourceDocument   `json:"documents,omitempty"`
	Metadata          Metadata           `json:"metadata"`
	Error             *RunError          `json:"error,omitempty"`

	LegacyPlan    *ResearchPlan   `json:"legacy_plan,omitempty"`
	ExecutedSteps []StepExecution `json:"executed_steps,omitempty"`
}

// Answer 是最终回答的用户可见摘要。
//
// Markdown 可以保存模型生成的完整正文；Summary/KeyFindings/Limitations 供 renderer
// 或调用方做结构化展示。
type Answer struct {
	Markdown    string   `json:"markdown"`
	Summary     string   `json:"summary"`
	KeyFindings []string `json:"key_findings"`
	Limitations []string `json:"limitations"`
}

// StepExecution 表示一个 ResearchStep 被多个 researcher 执行并综合后的结果。
//
// 它是 legacy step 流程和 todo 内部执行循环之间的桥接结构，后续会被转换为
// TodoExecution。
type StepExecution struct {
	Step              ResearchStep       `json:"step"`
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	Summary           string             `json:"summary"`
	Gaps              []string           `json:"gaps,omitempty"`
	Sources           []search.Source    `json:"sources"`
	Documents         []SourceDocument   `json:"documents,omitempty"`
}

// ResearcherResult 是单个 researcher agent 的原始研究输出。
//
// Sources/Documents/Findings 之后会经过 source ID 归一化和 evidence ref 补齐，因此
// agent 只需要尽力返回局部一致的引用即可。
type ResearcherResult struct {
	Role      string           `json:"role"`
	Focus     string           `json:"focus"`
	Queries   []string         `json:"queries"`
	Findings  []Finding        `json:"findings"`
	Sources   []search.Source  `json:"sources"`
	Documents []SourceDocument `json:"documents,omitempty"`
	Errors    []string         `json:"errors,omitempty"`
}

// Finding 是一个可被引用核验的研究判断。
//
// SourceIDs 保留 source-level citation；EvidenceRefs 进一步指向 chunk/quote，用于最终
// 报告中的 claim-level evidence。
type Finding struct {
	Claim        string        `json:"claim"`
	Rationale    string        `json:"rationale"`
	SourceIDs    []string      `json:"source_ids"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

// EvidenceRef 指向支撑某个 finding 的具体证据片段。
//
// ChunkID 和 Quote 可以缺省；证据归一化阶段会在能找到 SourceDocument 时自动补齐。
type EvidenceRef struct {
	SourceID string `json:"source_id"`
	ChunkID  string `json:"chunk_id,omitempty"`
	Quote    string `json:"quote,omitempty"`
}

// SourceDocument 是 Source 的可切片正文表示。
//
// 第一版会从搜索 snippet/title 或 web_fetch 正文构建 document；后续如果接入专门的
// crawler/parser，也应该继续落到这个结构上，保证 citation 层稳定。
type SourceDocument struct {
	ID       string        `json:"id"`
	SourceID string        `json:"source_id"`
	Title    string        `json:"title,omitempty"`
	URL      string        `json:"url"`
	Provider string        `json:"provider,omitempty"`
	Query    string        `json:"query,omitempty"`
	Chunks   []SourceChunk `json:"chunks"`
}

// SourceChunk 是 SourceDocument 中可被 evidence_refs 引用的最小文本片段。
//
// StartChar/EndChar 基于 rune 切分位置，主要用于调试和未来的高亮定位。
type SourceChunk struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	SourceID   string `json:"source_id"`
	Text       string `json:"text"`
	StartChar  int    `json:"start_char"`
	EndChar    int    `json:"end_char"`
}

// Metadata 记录一次 run 的运行环境和耗时，便于追踪模型、搜索 provider 与预算设置。
type Metadata struct {
	Model          string `json:"model"`
	SearchProvider string `json:"search_provider"`
	MaxIterations  int    `json:"max_iterations"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at"`
	DurationMS     int64  `json:"duration_ms"`
}

// RunError 保存结构化错误阶段，CLI 的 JSON 输出会依赖它向调用方暴露失败原因。
type RunError struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}
