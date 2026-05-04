package render

import (
	"fmt"
	"strings"

	"github.com/hu-quan-er/eino_research/internal/research"
)

func Markdown(result research.ResearchResult) string {
	if result.Answer.Markdown != "" {
		var sb strings.Builder
		sb.WriteString(result.Answer.Markdown)
		sb.WriteString("\n\n")
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
	appendSources(&sb, result)
	return strings.TrimSpace(sb.String()) + "\n"
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
