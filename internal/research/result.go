package research

import "github.com/hu-quan-er/eino_research/internal/search"

// ResearchResult 是一次 research run 的完整结构化输出。
//
// todo-plan 流程会填充 Plan、TodoExecutions、SectionExecutions、Sources 和 Documents。
type ResearchResult struct {
	// Question 是用户原始研究问题，会贯穿 planner、executor 和最终报告。
	Question string `json:"question"`
	// Answer 保存最终给用户看的回答摘要和 Markdown 正文。
	Answer Answer `json:"answer"`
	// Plan 是经过结构校验和质量 lint 后实际执行的 todo plan。
	Plan ResearchTodoPlan `json:"plan"`
	// SectionExecutions 是按 plan.sections 分组后的执行结果，主要供报告渲染。
	SectionExecutions []SectionExecution `json:"section_executions"`
	// TodoExecutions 是每个 todo 的原始执行结果，保留实际调度返回顺序。
	TodoExecutions []TodoExecution `json:"todo_executions"`
	// Sources 是所有 todo 产出的来源列表，按 URL 去重并尽量保留稳定 source_id。
	Sources []search.Source `json:"sources"`
	// Documents 是 sources 对应的可引用文本切片，用于 evidence quote 和后续审计。
	Documents []SourceDocument `json:"documents,omitempty"`
	// Metadata 记录模型、搜索 provider、耗时等运行信息。
	Metadata Metadata `json:"metadata"`
	// Error 在 run 失败时记录阶段和错误信息；成功时为空。
	Error *RunError `json:"error,omitempty"`
}

// Answer 是最终回答的用户可见摘要。
//
// Markdown 可以保存模型生成的完整正文；Summary/KeyFindings/Limitations 供 renderer
// 或调用方做结构化展示。Evidence 保存最终答案层面的 claim -> evidence 绑定结果。
type Answer struct {
	// Markdown 是完整回答正文；如果存在，Markdown renderer 会优先保留它。
	Markdown string `json:"markdown"`
	// Summary 是短摘要，供 CLI、JSON 消费方或默认 Markdown 报告使用。
	Summary string `json:"summary"`
	// KeyFindings 是可结构化展示的核心发现列表。
	KeyFindings []string `json:"key_findings"`
	// Limitations 是回答的已知限制、证据缺口或不确定性。
	Limitations []string `json:"limitations"`
	// Evidence 是最终答案中的关键 claim 对应的证据绑定结果。
	Evidence []ClaimEvidence `json:"evidence,omitempty"`
}

// priorResearchView 是 prior-context 喂给 researcher / synthesizer prompt 的精简投影。
//
// 它故意保持与历史 StepExecution 相同的 JSON 形状（step / researcher_results / summary /
// gaps / sources / documents），以确保统一执行结果类型后 prompt 内容字节级不变。
type priorResearchView struct {
	// Step 是该 prior 单元对应的 researcher 简报。
	Step ResearchStep `json:"step"`
	// ResearcherResults 是该 prior 单元的 researcher 原始输出。
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	// Summary 是该 prior 单元的综合摘要。
	Summary string `json:"summary"`
	// Gaps 是该 prior 单元仍未解决的问题。
	Gaps []string `json:"gaps,omitempty"`
	// Sources 是该 prior 单元的来源列表。
	Sources []search.Source `json:"sources"`
	// Documents 是该 prior 单元的可引用正文（dependency 投影不含，attempt 投影含）。
	Documents []SourceDocument `json:"documents,omitempty"`
}

// ResearcherResult 是单个 researcher agent 的原始研究输出。
//
// Sources/Documents/Findings 之后会经过 source ID 归一化和 evidence ref 补齐，因此
// agent 只需要尽力返回局部一致的引用即可。
type ResearcherResult struct {
	// Role 是 researcher 的稳定角色 ID，例如 evidence_researcher。
	Role string `json:"role"`
	// Focus 描述该 researcher 本轮负责的研究视角。
	Focus string `json:"focus"`
	// Queries 记录 researcher 实际使用或建议的搜索 query。
	Queries []string `json:"queries"`
	// Findings 是该 researcher 从其视角得到的 source-backed 判断。
	Findings []Finding `json:"findings"`
	// Sources 是该 researcher 直接返回的来源，source_id 之后可能被归一化重写。
	Sources []search.Source `json:"sources"`
	// Documents 是 researcher 提供的可引用正文片段，通常来自 web_fetch。
	Documents []SourceDocument `json:"documents,omitempty"`
	// Errors 记录 researcher 局部失败；只要不是所有 researcher 都失败，step 仍可继续。
	Errors []string `json:"errors,omitempty"`
}

// Finding 是一个可被引用核验的研究判断。
//
// SourceIDs 保留 source-level citation；EvidenceRefs 进一步指向 chunk/quote，用于最终
// 报告中的 claim-level evidence。
type Finding struct {
	// Claim 是可以被证据支撑或反驳的明确判断。
	Claim string `json:"claim"`
	// Rationale 解释为什么该证据支持 Claim。
	Rationale string `json:"rationale"`
	// SourceIDs 指向支撑该判断的 source-level 来源。
	SourceIDs []string `json:"source_ids"`
	// EvidenceRefs 指向更细粒度的 chunk/quote 证据。
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

// EvidenceRef 指向支撑某个 finding 的具体证据片段。
//
// ChunkID 和 Quote 可以缺省；证据归一化阶段会在能找到 SourceDocument 时自动补齐。
type EvidenceRef struct {
	// SourceID 引用 Sources 或 Documents 中的 source_id。
	SourceID string `json:"source_id"`
	// ChunkID 引用 SourceDocument.Chunks 中的 chunk id，可由系统自动补齐。
	ChunkID string `json:"chunk_id,omitempty"`
	// Quote 是用于报告展示的短证据摘录，可由模型提供或系统从 chunk 中截取。
	Quote string `json:"quote,omitempty"`
}

// ClaimEvidence 描述最终答案中某个 claim 与证据片段的绑定关系。
//
// 它不同于 todo-level Finding：Finding 是研究过程中的中间发现，ClaimEvidence 是最终
// Answer 级别的核验结果，用于判断最终报告中的关键表述是否能回到具体来源。
type ClaimEvidence struct {
	// Claim 是从最终答案中抽取或规范化后的关键判断。
	Claim string `json:"claim"`
	// SourceIDs 是该 claim 绑定到的来源 ID。
	SourceIDs []string `json:"source_ids,omitempty"`
	// EvidenceRefs 是该 claim 对应的 chunk/quote 证据。
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
	// Supported 表示当前系统是否找到可用证据支撑该 claim。
	Supported bool `json:"supported"`
	// Reason 记录绑定或无法绑定的原因，便于调试引用质量。
	Reason string `json:"reason,omitempty"`
}

// SourceDocument 是 Source 的可切片正文表示。
//
// 第一版会从搜索 snippet/title 或 web_fetch 正文构建 document；后续如果接入专门的
// crawler/parser，也应该继续落到这个结构上，保证 citation 层稳定。
type SourceDocument struct {
	// ID 是 document 自身的稳定 ID，通常由 source_id + "_doc" 生成。
	ID string `json:"id"`
	// SourceID 把 document 绑定回 search.Source.ID。
	SourceID string `json:"source_id"`
	// Title 是来源页面或搜索结果标题。
	Title string `json:"title,omitempty"`
	// URL 是来源页面地址，也是 document 去重的重要依据。
	URL string `json:"url"`
	// Provider 记录来源 provider，例如 google、mock 或未来的其他检索源。
	Provider string `json:"provider,omitempty"`
	// Query 记录发现该 source 的搜索 query，便于调试检索覆盖面。
	Query string `json:"query,omitempty"`
	// Chunks 是可被 evidence_refs 引用的正文切片。
	Chunks []SourceChunk `json:"chunks"`
}

// SourceChunk 是 SourceDocument 中可被 evidence_refs 引用的最小文本片段。
//
// StartChar/EndChar 基于 rune 切分位置，主要用于调试和未来的高亮定位。
type SourceChunk struct {
	// ID 是 chunk 的稳定 ID，通常由 source_id + "_chunk_N" 生成。
	ID string `json:"id"`
	// DocumentID 指向所属 SourceDocument.ID。
	DocumentID string `json:"document_id"`
	// SourceID 冗余保存 source id，方便只拿到 chunk 时也能回到 source。
	SourceID string `json:"source_id"`
	// Text 是该 chunk 的正文。
	Text string `json:"text"`
	// StartChar 是 chunk 在 document 文本中的起始 rune offset。
	StartChar int `json:"start_char"`
	// EndChar 是 chunk 在 document 文本中的结束 rune offset。
	EndChar int `json:"end_char"`
}

// Metadata 记录一次 run 的运行环境和耗时，便于追踪模型、搜索 provider 与预算设置。
type Metadata struct {
	// Model 是本次运行使用的模型名称。
	Model string `json:"model"`
	// SearchProvider 是本次运行使用的搜索 provider 名称。
	SearchProvider string `json:"search_provider"`
	// MaxTodoResearchIterations 是单个 todo 内 gap retry 的最大轮数。
	MaxTodoResearchIterations int `json:"max_todo_research_iterations"`
	// StartedAt 是 run 开始时间，使用 RFC3339 字符串。
	StartedAt string `json:"started_at"`
	// CompletedAt 是 run 结束时间，使用 RFC3339 字符串。
	CompletedAt string `json:"completed_at"`
	// DurationMS 是 run 总耗时，单位毫秒。
	DurationMS int64 `json:"duration_ms"`
	// Trace 是事件追踪记录，由 Runner 在 Execute 结束时从内置 TraceStore 写入。
	Trace []Event `json:"trace,omitempty"`
	// Budget 是事件累计统计，由 Runner 在 Execute 结束时从内置 BudgetMeter 写入。
	Budget *BudgetReport `json:"budget,omitempty"`
}

// RunError 保存结构化错误阶段，CLI 的 JSON 输出会依赖它向调用方暴露失败原因。
type RunError struct {
	// Stage 标识错误发生阶段，例如 input、plan、execute、finalize。
	Stage string `json:"stage"`
	// Message 是可展示或记录的错误详情。
	Message string `json:"message"`
}
