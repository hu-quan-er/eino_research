package render

import (
	"fmt"
	"strings"

	"github.com/hu-quan-er/eino_research/internal/research"
)

func Markdown(result research.ResearchResult) string {
	if result.Answer.Markdown != "" {
		var sb strings.Builder
		sb.WriteString(strings.TrimSpace(result.Answer.Markdown))
		sb.WriteString("\n\n")
		appendExecutionSummary(&sb, result)
		appendFindingsAndEvidence(&sb, result)
		appendSources(&sb, result)
		return strings.TrimSpace(sb.String()) + "\n"
	}

	var sb strings.Builder
	sb.WriteString("# Research Report\n\n")
	sb.WriteString("## Question\n\n")
	sb.WriteString(result.Question)
	sb.WriteString("\n\n## Summary\n\n")
	sb.WriteString(result.Answer.Summary)
	sb.WriteString("\n\n")
	appendAnswerDetails(&sb, result.Answer)
	appendExecutionSummary(&sb, result)
	appendFindingsAndEvidence(&sb, result)
	appendSources(&sb, result)
	return strings.TrimSpace(sb.String()) + "\n"
}

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

func todoSummaryTitle(todo research.ResearchTodo) string {
	if title := strings.TrimSpace(todo.Title); title != "" {
		return title
	}
	if question := strings.TrimSpace(todo.Question); question != "" {
		return question
	}
	return todo.ID
}

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
