package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

// FinalSynthesizer 负责把所有 todo 执行结果综合为最终用户答案。
//
// 它位于 todo scheduler 之后，能看到完整 plan、section、todo、source 和 document，
// 因此适合处理跨 todo 的结论归纳、冲突说明、限制条件和最终 Markdown 报告。
type FinalSynthesizer interface {
	SynthesizeFinal(ctx context.Context, in FinalSynthesisInput) (Answer, error)
}

// FinalSynthesisInput 是最终综合阶段的完整上下文。
type FinalSynthesisInput struct {
	// Question 是用户原始问题。
	Question string `json:"question"`
	// Plan 是实际执行的研究计划。
	Plan ResearchTodoPlan `json:"plan"`
	// SectionExecutions 是按 plan section 分组后的执行结果。
	SectionExecutions []SectionExecution `json:"section_executions"`
	// TodoExecutions 是所有 todo 的执行结果。
	TodoExecutions []TodoExecution `json:"todo_executions"`
	// Sources 是最终去重后的来源列表。
	Sources []search.Source `json:"sources"`
	// Documents 是可引用的正文切片。
	Documents []SourceDocument `json:"documents,omitempty"`
	// SectionAnswers 是 section 级归纳产物；非空时 Final 上下文走紧凑路径，不再 dump 全部 todo。
	SectionAnswers []SectionAnswer `json:"section_answers,omitempty"`
}

// AgentFinalSynthesizer 使用模型生成最终全局报告。
type AgentFinalSynthesizer struct {
	// model 是用于最终综合的 chat model。
	model model.BaseChatModel
}

// NewAgentFinalSynthesizer 创建默认最终综合器。
func NewAgentFinalSynthesizer(m model.BaseChatModel) *AgentFinalSynthesizer {
	return &AgentFinalSynthesizer{model: m}
}

// SynthesizeFinal 将 todo 层研究结果综合为 Answer。
//
// 模型必须返回 Answer JSON；如果模型输出不是合法 Answer，会保留原始文本或执行摘要作为
// 兜底答案，避免前面已经完成的研究结果被最终格式问题整体吞掉。
func (s *AgentFinalSynthesizer) SynthesizeFinal(ctx context.Context, in FinalSynthesisInput) (Answer, error) {
	if s == nil || isNilDependency(s.model) {
		return Answer{}, fmt.Errorf("final synthesizer model is nil")
	}

	b, err := json.Marshal(buildFinalSynthesisContext(in))
	if err != nil {
		return Answer{}, fmt.Errorf("marshal final synthesis input: %w", err)
	}

	resp, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(`You are the final synthesis agent for a deep research workflow.

Write the final answer in the same language as the user's question. Use only the provided todo results, sources, and documents. Do not invent facts. Cite source-backed claims with source IDs like [src_1] or [todo_evidence_src_1]. Move unsupported or weakly supported claims to limitations.

Return only one JSON object matching:
{
  "markdown": string,
  "summary": string,
  "key_findings": [string],
  "limitations": [string]
}

The markdown should be a complete report with a clear conclusion first, then evidence-backed findings, conflicts or uncertainty, and practical implications when relevant.`),
		schema.UserMessage(string(b)),
	})
	if err != nil {
		return Answer{}, err
	}
	if resp == nil {
		return Answer{}, fmt.Errorf("model response is nil")
	}

	content := strings.TrimSpace(resp.Content)
	answer, err := parseFinalAnswer(content)
	if err != nil {
		rawOutput := content
		if json.Valid([]byte(content)) {
			rawOutput = ""
		}
		return fallbackFinalAnswer(in, rawOutput, err), nil
	}
	return normalizeFinalAnswer(answer, in), nil
}

// synthesizeFinalAnswer 调用配置中的最终综合器，并在综合器失败时生成确定性兜底答案。
func (r *Runner) synthesizeFinalAnswer(ctx context.Context, in FinalSynthesisInput) Answer {
	synthesizer := r.cfg.FinalSynthesizer
	if synthesizer == nil {
		synthesizer = NewAgentFinalSynthesizer(r.cfg.Model)
	}

	answer, err := synthesizer.SynthesizeFinal(ctx, in)
	if err != nil {
		return fallbackFinalAnswer(in, "", err)
	}
	return normalizeFinalAnswer(answer, in)
}

// parseFinalAnswer 解析模型返回的 Answer，并拒绝空 Answer。
func parseFinalAnswer(content string) (Answer, error) {
	if strings.TrimSpace(content) == "" {
		return Answer{}, fmt.Errorf("final answer output is empty")
	}
	var answer Answer
	if err := json.Unmarshal([]byte(content), &answer); err != nil {
		return Answer{}, fmt.Errorf("invalid final Answer JSON: %w", err)
	}
	if isEmptyAnswer(answer) {
		return Answer{}, fmt.Errorf("final Answer JSON contains no answer fields")
	}
	return answer, nil
}

// normalizeFinalAnswer 补齐 Answer 的 summary/markdown，保证 renderer 总有可展示内容。
func normalizeFinalAnswer(answer Answer, in FinalSynthesisInput) Answer {
	answer.Markdown = strings.TrimSpace(answer.Markdown)
	answer.Summary = strings.TrimSpace(answer.Summary)
	answer.KeyFindings = trimNonEmptyStrings(answer.KeyFindings)
	answer.Limitations = trimNonEmptyStrings(answer.Limitations)

	if answer.Summary == "" {
		switch {
		case len(answer.KeyFindings) > 0:
			answer.Summary = answer.KeyFindings[0]
		case answer.Markdown != "":
			answer.Summary = firstNonEmptyLine(answer.Markdown)
		default:
			answer.Summary = completedTodosSummary(in.TodoExecutions)
		}
	}
	if answer.Markdown == "" {
		answer.Markdown = buildFallbackFinalMarkdown(in, answer)
	}
	return answer
}

// fallbackFinalAnswer 在模型输出无效或最终综合器出错时构造确定性答案。
func fallbackFinalAnswer(in FinalSynthesisInput, rawOutput string, cause error) Answer {
	answer := Answer{
		Summary:     completedTodosSummary(in.TodoExecutions),
		KeyFindings: collectTopFindingClaims(in.TodoExecutions, 6),
		Limitations: collectFinalLimitations(in.TodoExecutions),
	}
	if cause != nil {
		answer.Limitations = append(answer.Limitations, "Final synthesis fallback was used: "+cause.Error())
	}
	if text := strings.TrimSpace(rawOutput); text != "" {
		answer.Markdown = text
	}
	return normalizeFinalAnswer(answer, in)
}

// buildFinalSynthesisContext 压缩最终综合输入，避免把重复结构直接塞给模型。
//
// 有 section answers 时走紧凑路径（只放 section answers + sources + documents）；否则回退到
// 把全部 todo 结果 dump 给模型的旧路径。
func buildFinalSynthesisContext(in FinalSynthesisInput) finalSynthesisContext {
	ctx := finalSynthesisContext{
		Question:  in.Question,
		Objective: in.Plan.Objective,
		Sources:   in.Sources,
		Documents: in.Documents,
	}
	if len(in.SectionAnswers) > 0 {
		ctx.SectionAnswers = in.SectionAnswers
		return ctx
	}

	sections := make([]finalSectionSynthesisContext, 0, len(in.SectionExecutions))
	for _, section := range in.SectionExecutions {
		todos := make([]finalTodoSynthesisContext, 0, len(section.Todos))
		for _, todo := range section.Todos {
			todos = append(todos, finalTodoSynthesisContext{
				ID:                 todo.Todo.ID,
				Title:              todo.Todo.Title,
				Question:           todo.Todo.Question,
				AcceptanceCriteria: todo.Todo.AcceptanceCriteria,
				Status:             todo.Status,
				Summary:            todo.Summary,
				Findings:           todo.Findings,
				Gaps:               todo.Gaps,
				Error:              todo.Error,
				ResearcherResults:  compactResearcherResults(todo.ResearcherResults),
			})
		}
		sections = append(sections, finalSectionSynthesisContext{
			ID:      section.Section.ID,
			Title:   section.Section.Title,
			Summary: section.Summary,
			Todos:   todos,
		})
	}
	ctx.Sections = sections
	return ctx
}

// finalSynthesisContext 是给最终综合模型的压缩上下文。
type finalSynthesisContext struct {
	// Question 是用户原始问题，提示模型保持回答范围和语言一致。
	Question string `json:"question"`
	// Objective 是 planner 提炼出的全局目标，比原问题更适合作为跨 todo 汇总轴。
	Objective string `json:"objective"`
	// SectionAnswers 是 section 级归纳产物；非空时作为紧凑路径的主要内容，替代全部 todo dump。
	SectionAnswers []SectionAnswer `json:"section_answers,omitempty"`
	// Sections 是按报告章节压缩后的 todo 结果；仅在没有 section answers 的回退路径填充。
	Sections []finalSectionSynthesisContext `json:"sections,omitempty"`
	// Sources 是最终去重后的来源元数据，用于模型写内联 source id。
	Sources []search.Source `json:"sources"`
	// Documents 是可引用正文切片，用于模型核对 quote 和避免编造事实。
	Documents []SourceDocument `json:"documents,omitempty"`
}

// finalSectionSynthesisContext 是单个 section 的压缩上下文。
type finalSectionSynthesisContext struct {
	// ID 是 section 稳定标识。
	ID string `json:"id"`
	// Title 是 section 展示标题。
	Title string `json:"title"`
	// Summary 是该 section 的确定性摘要，来自 todo summaries 拼接。
	Summary string `json:"summary"`
	// Todos 是 section 下每个 todo 的压缩执行结果。
	Todos []finalTodoSynthesisContext `json:"todos"`
}

// finalTodoSynthesisContext 是单个 todo 的压缩执行快照。
type finalTodoSynthesisContext struct {
	// ID 是 todo 稳定标识，用于最终答案引用和问题定位。
	ID string `json:"id"`
	// Title 是 todo 展示标题。
	Title string `json:"title"`
	// Question 是该 todo 具体回答的问题。
	Question string `json:"question"`
	// AcceptanceCriteria 是 planner 给出的完成标准，帮助最终综合判断覆盖是否足够。
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	// Status 是调度器给出的终态。
	Status TodoStatus `json:"status"`
	// Summary 是 todo 内 synthesizer 的综合结果。
	Summary string `json:"summary"`
	// Findings 是该 todo 的扁平化证据发现。
	Findings []Finding `json:"findings,omitempty"`
	// Gaps 是 todo 仍未解决的证据或问题缺口。
	Gaps []string `json:"gaps,omitempty"`
	// Error 是 failed/blocked/skipped 的原因。
	Error string `json:"error,omitempty"`
	// ResearcherResults 保留角色视角和局部错误，但移除重复 sources 以控制上下文体积。
	ResearcherResults []finalResearcherSnapshot `json:"researcher_results,omitempty"`
}

// finalResearcherSnapshot 是最终综合阶段保留的 researcher 轻量快照。
type finalResearcherSnapshot struct {
	// Role 是 researcher 角色 ID。
	Role string `json:"role"`
	// Focus 是该角色的研究范围。
	Focus string `json:"focus"`
	// Findings 是该角色贡献的判断和证据引用。
	Findings []Finding `json:"findings,omitempty"`
	// Errors 是该角色局部失败信息。
	Errors []string `json:"errors,omitempty"`
}

// compactResearcherResults 保留最终综合需要的 researcher 视角、发现和错误，去掉重复 sources。
func compactResearcherResults(results []ResearcherResult) []finalResearcherSnapshot {
	out := make([]finalResearcherSnapshot, 0, len(results))
	for _, result := range results {
		out = append(out, finalResearcherSnapshot{
			Role:     result.Role,
			Focus:    result.Focus,
			Findings: result.Findings,
			Errors:   result.Errors,
		})
	}
	return out
}

// isEmptyAnswer 判断模型是否返回了语法合法但业务上空的 Answer。
func isEmptyAnswer(answer Answer) bool {
	return strings.TrimSpace(answer.Markdown) == "" &&
		strings.TrimSpace(answer.Summary) == "" &&
		len(trimNonEmptyStrings(answer.KeyFindings)) == 0 &&
		len(trimNonEmptyStrings(answer.Limitations)) == 0
}

// completedTodosSummary 生成兜底 summary，主要用于最终综合失败时仍能展示执行进展。
func completedTodosSummary(executions []TodoExecution) string {
	return fmt.Sprintf("Completed %d todo(s).", countTodoStatus(executions, TodoDone))
}

// collectTopFindingClaims 从 todo findings 中抽取前 N 条不同 claim 作为兜底 key findings。
func collectTopFindingClaims(executions []TodoExecution, limit int) []string {
	if limit <= 0 {
		return nil
	}
	claims := make([]string, 0, limit)
	seen := make(map[string]struct{})
	for _, execution := range executions {
		for _, finding := range execution.Findings {
			claim := strings.TrimSpace(finding.Claim)
			if claim == "" {
				continue
			}
			key := strings.ToLower(claim)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			claims = append(claims, claimWithSourceIDs(claim, finding.SourceIDs))
			if len(claims) >= limit {
				return claims
			}
		}
	}
	return claims
}

// collectFinalLimitations 汇总 todo gaps 和失败原因，作为最终答案限制说明。
func collectFinalLimitations(executions []TodoExecution) []string {
	limitations := make([]string, 0)
	for _, execution := range executions {
		for _, gap := range execution.Gaps {
			if gap = strings.TrimSpace(gap); gap != "" {
				limitations = append(limitations, fmt.Sprintf("%s: %s", todoSummaryTitleForAnswer(execution.Todo), gap))
			}
		}
		if execution.Status != TodoDone && strings.TrimSpace(execution.Error) != "" {
			limitations = append(limitations, fmt.Sprintf("%s: %s", todoSummaryTitleForAnswer(execution.Todo), execution.Error))
		}
	}
	return dedupeStrings(trimNonEmptyStrings(limitations))
}

// claimWithSourceIDs 把 source id 追加到 claim 后，保留最基本的 citation 可追踪性。
func claimWithSourceIDs(claim string, sourceIDs []string) string {
	sourceIDs = trimNonEmptyStrings(sourceIDs)
	if len(sourceIDs) == 0 {
		return claim
	}
	return claim + " [" + strings.Join(sourceIDs, ", ") + "]"
}

// buildFallbackFinalMarkdown 用结构化 Answer 构造 Markdown 正文。
//
// 该路径只在模型无正文或最终综合失败时使用，目标是保留已有研究成果而不是追求文采。
func buildFallbackFinalMarkdown(in FinalSynthesisInput, answer Answer) string {
	var sb strings.Builder
	sb.WriteString("# Research Report\n\n")
	sb.WriteString("## Summary\n\n")
	sb.WriteString(answer.Summary)
	sb.WriteString("\n\n")
	if len(answer.KeyFindings) > 0 {
		sb.WriteString("## Key Findings\n\n")
		for _, finding := range answer.KeyFindings {
			sb.WriteString("- ")
			sb.WriteString(finding)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if len(answer.Limitations) > 0 {
		sb.WriteString("## Limitations\n\n")
		for _, limitation := range answer.Limitations {
			sb.WriteString("- ")
			sb.WriteString(limitation)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if len(in.SectionExecutions) > 0 {
		sb.WriteString("## Section Notes\n\n")
		for _, section := range in.SectionExecutions {
			title := strings.TrimSpace(section.Section.Title)
			if title == "" {
				title = section.Section.ID
			}
			if title == "" {
				title = "Untitled Section"
			}
			sb.WriteString("### ")
			sb.WriteString(title)
			sb.WriteString("\n\n")
			if summary := strings.TrimSpace(section.Summary); summary != "" {
				sb.WriteString(summary)
				sb.WriteString("\n\n")
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// firstNonEmptyLine 从 Markdown/纯文本中提取第一条可用摘要。
func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

// trimNonEmptyStrings 清理字符串数组，去掉空白项但保留原顺序。
func trimNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// todoSummaryTitleForAnswer 为 limitations 选择可读 todo 标题。
func todoSummaryTitleForAnswer(todo ResearchTodo) string {
	if title := strings.TrimSpace(todo.Title); title != "" {
		return title
	}
	if question := strings.TrimSpace(todo.Question); question != "" {
		return question
	}
	if id := strings.TrimSpace(todo.ID); id != "" {
		return id
	}
	return "todo"
}
