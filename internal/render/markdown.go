package render

import (
	"fmt"
	"strings"

	"github.com/hu-quan-er/eino_research/internal/research"
)

// Markdown 将 ResearchResult 渲染为终端友好的 Markdown 报告。
//
// 如果模型已经给出 Answer.Markdown，会保留正文并追加执行摘要、证据和 sources；否则会从
// 结构化字段生成标准报告。
func Markdown(result research.ResearchResult) string {
	if result.Answer.Markdown != "" {
		// FinalSynthesizer 已经产出正文时，不重写主报告，只追加可审计的执行和证据附录。
		var sb strings.Builder
		sb.WriteString(strings.TrimSpace(result.Answer.Markdown))
		sb.WriteString("\n\n")
		appendAnswerEvidence(&sb, result)
		appendExecutionSummary(&sb, result)
		appendFindingsAndEvidence(&sb, result)
		appendSources(&sb, result)
		return strings.TrimSpace(sb.String()) + "\n"
	}

	// 没有模型正文时，从结构化 Answer 字段生成标准模板，保证 CLI 仍有可读输出。
	var sb strings.Builder
	sb.WriteString("# Research Report\n\n")
	sb.WriteString("## Question\n\n")
	sb.WriteString(result.Question)
	sb.WriteString("\n\n## Summary\n\n")
	sb.WriteString(result.Answer.Summary)
	sb.WriteString("\n\n")
	appendAnswerDetails(&sb, result.Answer)
	appendAnswerEvidence(&sb, result)
	appendExecutionSummary(&sb, result)
	appendFindingsAndEvidence(&sb, result)
	appendSources(&sb, result)
	return strings.TrimSpace(sb.String()) + "\n"
}

// appendAnswerDetails 渲染结构化 key findings 和 limitations。
func appendAnswerDetails(sb *strings.Builder, answer research.Answer) {
	if len(answer.KeyFindings) > 0 {
		sb.WriteString("## Key Findings\n\n")
		for _, finding := range answer.KeyFindings {
			if finding = inlineText(finding); finding != "" {
				sb.WriteString("- ")
				sb.WriteString(finding)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	if len(answer.Limitations) > 0 {
		sb.WriteString("## Limitations\n\n")
		for _, limitation := range answer.Limitations {
			if limitation = inlineText(limitation); limitation != "" {
				sb.WriteString("- ")
				sb.WriteString(limitation)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}
}

// appendExecutionSummary 渲染按 section 分组的 todo 执行状态。
func appendExecutionSummary(sb *strings.Builder, result research.ResearchResult) {
	if len(result.SectionExecutions) == 0 {
		return
	}

	sb.WriteString("## Execution Summary\n\n")
	for _, section := range result.SectionExecutions {
		title := strings.TrimSpace(section.Section.Title)
		if title == "" {
			title = section.Section.ID
		}
		if title == "" {
			title = "Untitled Section"
		}
		sb.WriteString("### ")
		sb.WriteString(title)
		sb.WriteString("\n")
		for _, todo := range section.Todos {
			sb.WriteString(fmt.Sprintf("- %s %s: %s\n", todo.Status, todo.Todo.ID, todoSummaryTitle(todo.Todo)))
		}
		sb.WriteString("\n")
	}
}

// todoSummaryTitle 选择 todo 在报告中展示的标题。
func todoSummaryTitle(todo research.ResearchTodo) string {
	if title := strings.TrimSpace(todo.Title); title != "" {
		return title
	}
	if question := strings.TrimSpace(todo.Question); question != "" {
		return question
	}
	return todo.ID
}

// appendSources 渲染最终去重后的 source 列表。
func appendSources(sb *strings.Builder, result research.ResearchResult) {
	if len(result.Sources) == 0 {
		return
	}
	sb.WriteString("## Sources\n\n")
	for _, src := range result.Sources {
		sb.WriteString(fmt.Sprintf("- [%s] %s", src.ID, src.Title))
		if src.URL != "" {
			sb.WriteString(fmt.Sprintf(" - %s", src.URL))
		}
		sb.WriteString("\n")
	}
}
