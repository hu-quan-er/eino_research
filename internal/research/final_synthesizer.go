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
func buildFinalSynthesisContext(in FinalSynthesisInput) finalSynthesisContext {
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

	return finalSynthesisContext{
		Question:  in.Question,
		Objective: in.Plan.Objective,
		Sections:  sections,
		Sources:   in.Sources,
		Documents: in.Documents,
	}
}

type finalSynthesisContext struct {
	Question  string                         `json:"question"`
	Objective string                         `json:"objective"`
	Sections  []finalSectionSynthesisContext `json:"sections"`
	Sources   []search.Source                `json:"sources"`
	Documents []SourceDocument               `json:"documents,omitempty"`
}

type finalSectionSynthesisContext struct {
	ID      string                      `json:"id"`
	Title   string                      `json:"title"`
	Summary string                      `json:"summary"`
	Todos   []finalTodoSynthesisContext `json:"todos"`
}

type finalTodoSynthesisContext struct {
	ID                 string                    `json:"id"`
	Title              string                    `json:"title"`
	Question           string                    `json:"question"`
	AcceptanceCriteria []string                  `json:"acceptance_criteria,omitempty"`
	Status             TodoStatus                `json:"status"`
	Summary            string                    `json:"summary"`
	Findings           []Finding                 `json:"findings,omitempty"`
	Gaps               []string                  `json:"gaps,omitempty"`
	Error              string                    `json:"error,omitempty"`
	ResearcherResults  []finalResearcherSnapshot `json:"researcher_results,omitempty"`
}

type finalResearcherSnapshot struct {
	Role     string    `json:"role"`
	Focus    string    `json:"focus"`
	Findings []Finding `json:"findings,omitempty"`
	Errors   []string  `json:"errors,omitempty"`
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

func isEmptyAnswer(answer Answer) bool {
	return strings.TrimSpace(answer.Markdown) == "" &&
		strings.TrimSpace(answer.Summary) == "" &&
		len(trimNonEmptyStrings(answer.KeyFindings)) == 0 &&
		len(trimNonEmptyStrings(answer.Limitations)) == 0
}

func completedTodosSummary(executions []TodoExecution) string {
	return fmt.Sprintf("Completed %d todo(s).", countTodoStatus(executions, TodoDone))
}

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

func claimWithSourceIDs(claim string, sourceIDs []string) string {
	sourceIDs = trimNonEmptyStrings(sourceIDs)
	if len(sourceIDs) == 0 {
		return claim
	}
	return claim + " [" + strings.Join(sourceIDs, ", ") + "]"
}

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

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

func trimNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

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
