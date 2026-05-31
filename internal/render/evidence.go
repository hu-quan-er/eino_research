package render

import (
	"strings"

	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
)

// maxRenderedEvidenceQuoteChars 限制 Markdown 中单条证据 quote 的长度，避免列表项过长。
const maxRenderedEvidenceQuoteChars = 360

// appendFindingsAndEvidence 按 todo 输出 findings 和对应证据。
//
// 这里优先使用 Finding.EvidenceRefs 中的 quote；缺失时会从 result.Documents 中查找 source
// 对应的 chunk 作为兜底。
func appendFindingsAndEvidence(sb *strings.Builder, result research.ResearchResult) {
	todos := renderedTodoExecutions(result)
	if len(todos) == 0 {
		return
	}

	index := buildEvidenceIndex(result)
	wroteHeader := false
	for _, todo := range todos {
		findings := todoFindings(todo)
		if len(findings) == 0 {
			continue
		}
		if !wroteHeader {
			sb.WriteString("## Findings and Evidence\n\n")
			wroteHeader = true
		}

		sb.WriteString("### ")
		if todo.Todo.ID != "" {
			sb.WriteString(todo.Todo.ID)
			sb.WriteString(": ")
		}
		sb.WriteString(todoSummaryTitle(todo.Todo))
		sb.WriteString("\n\n")

		for _, finding := range findings {
			appendFinding(sb, finding, index)
		}
		sb.WriteString("\n")
	}
}

// appendAnswerEvidence 渲染最终答案层面的 claim -> evidence 绑定结果。
func appendAnswerEvidence(sb *strings.Builder, result research.ResearchResult) {
	if len(result.Answer.Evidence) == 0 {
		return
	}

	index := buildEvidenceIndex(result)
	sb.WriteString("## Answer Evidence\n\n")
	for _, evidence := range result.Answer.Evidence {
		claim := inlineText(evidence.Claim)
		if claim == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(claim)
		if ids := claimEvidenceCitationIDs(evidence); len(ids) > 0 {
			sb.WriteString(" ")
			sb.WriteString(formatCitationIDs(ids))
		}
		if evidence.Supported {
			sb.WriteString(" (supported)")
		} else {
			sb.WriteString(" (unsupported)")
		}
		sb.WriteString("\n")
		if reason := inlineText(evidence.Reason); reason != "" {
			sb.WriteString("  - Reason: ")
			sb.WriteString(reason)
			sb.WriteString("\n")
		}
		for _, ref := range evidenceRefsForClaimEvidence(evidence, index) {
			if rendered := renderEvidenceRef(ref, index); rendered != "" {
				sb.WriteString("  - Evidence: ")
				sb.WriteString(rendered)
				sb.WriteString("\n")
			}
		}
	}
	sb.WriteString("\n")
}

// evidenceIndex 是 Markdown 渲染阶段使用的查找表，用于把 source_id/chunk_id 快速映射回
// source 元数据和 chunk 文本。
type evidenceIndex struct {
	// sourceByID 用 source_id 查 source 元数据。
	sourceByID map[string]search.Source
	// documentBySourceID 用 source_id 查对应 document。
	documentBySourceID map[string]research.SourceDocument
	// chunkByID 用 chunk_id 查具体文本片段。
	chunkByID map[string]research.SourceChunk
	// firstChunkBySource 是 source_id 到首个 chunk 的兜底映射。
	firstChunkBySource map[string]research.SourceChunk
}

// buildEvidenceIndex 为一次渲染构建 evidence 查找表。
func buildEvidenceIndex(result research.ResearchResult) evidenceIndex {
	index := evidenceIndex{
		sourceByID:         make(map[string]search.Source, len(result.Sources)),
		documentBySourceID: make(map[string]research.SourceDocument, len(result.Documents)),
		chunkByID:          make(map[string]research.SourceChunk),
		firstChunkBySource: make(map[string]research.SourceChunk, len(result.Documents)),
	}
	for _, source := range result.Sources {
		if id := strings.TrimSpace(source.ID); id != "" {
			index.sourceByID[id] = source
		}
	}
	for _, document := range result.Documents {
		sourceID := strings.TrimSpace(document.SourceID)
		if sourceID != "" {
			if _, ok := index.documentBySourceID[sourceID]; !ok {
				// 同一 source 可能同时有 fetched document 和 snippet document；保留第一个与上游优先级一致。
				index.documentBySourceID[sourceID] = document
			}
		}
		for _, chunk := range document.Chunks {
			if id := strings.TrimSpace(chunk.ID); id != "" {
				index.chunkByID[id] = chunk
			}
			if sourceID == "" {
				continue
			}
			if _, ok := index.firstChunkBySource[sourceID]; !ok {
				index.firstChunkBySource[sourceID] = chunk
			}
		}
	}
	return index
}

// renderedTodoExecutions 选择用于渲染的 todo executions。
//
// 新流程优先使用 result.TodoExecutions；旧结果只有 SectionExecutions 时则从 section 中展开。
func renderedTodoExecutions(result research.ResearchResult) []research.TodoExecution {
	if len(result.TodoExecutions) > 0 {
		return result.TodoExecutions
	}

	var todos []research.TodoExecution
	for _, section := range result.SectionExecutions {
		todos = append(todos, section.Todos...)
	}
	return todos
}

// todoFindings 获取 todo 已聚合的 findings；缺失时回退到 researcher results。
func todoFindings(todo research.TodoExecution) []research.Finding {
	if len(todo.Findings) > 0 {
		return todo.Findings
	}

	var findings []research.Finding
	for _, result := range todo.ResearcherResults {
		findings = append(findings, result.Findings...)
	}
	return findings
}

// appendFinding 渲染单条 finding，包括 claim、rationale 和 evidence quote。
func appendFinding(sb *strings.Builder, finding research.Finding, index evidenceIndex) {
	claim := inlineText(finding.Claim)
	if claim == "" {
		return
	}

	sb.WriteString("- ")
	sb.WriteString(claim)
	if ids := findingCitationIDs(finding); len(ids) > 0 {
		sb.WriteString(" ")
		sb.WriteString(formatCitationIDs(ids))
	}
	sb.WriteString("\n")

	if rationale := inlineText(finding.Rationale); rationale != "" {
		sb.WriteString("  - Rationale: ")
		sb.WriteString(rationale)
		sb.WriteString("\n")
	}

	for _, ref := range evidenceRefsForFinding(finding, index) {
		if rendered := renderEvidenceRef(ref, index); rendered != "" {
			sb.WriteString("  - Evidence: ")
			sb.WriteString(rendered)
			sb.WriteString("\n")
		}
	}
}

// findingCitationIDs 合并 finding 的 source_ids 和 evidence_refs.source_id。
func findingCitationIDs(finding research.Finding) []string {
	ids := make([]string, 0, len(finding.SourceIDs)+len(finding.EvidenceRefs))
	ids = append(ids, finding.SourceIDs...)
	for _, ref := range finding.EvidenceRefs {
		ids = append(ids, ref.SourceID)
	}
	return dedupeInlineStrings(ids)
}

// claimEvidenceCitationIDs 合并 ClaimEvidence 的 source_ids 和 evidence_refs.source_id。
func claimEvidenceCitationIDs(evidence research.ClaimEvidence) []string {
	ids := make([]string, 0, len(evidence.SourceIDs)+len(evidence.EvidenceRefs))
	ids = append(ids, evidence.SourceIDs...)
	for _, ref := range evidence.EvidenceRefs {
		ids = append(ids, ref.SourceID)
	}
	return dedupeInlineStrings(ids)
}

// evidenceRefsForFinding 返回 finding 可渲染的 evidence_refs，并在必要时从 document chunk
// 自动补齐 quote。
func evidenceRefsForFinding(finding research.Finding, index evidenceIndex) []research.EvidenceRef {
	if len(finding.EvidenceRefs) == 0 {
		refs := make([]research.EvidenceRef, 0, len(finding.SourceIDs))
		for _, sourceID := range finding.SourceIDs {
			sourceID = strings.TrimSpace(sourceID)
			if sourceID == "" {
				continue
			}
			ref := research.EvidenceRef{SourceID: sourceID}
			if chunk, ok := index.firstChunkBySource[sourceID]; ok {
				ref.ChunkID = chunk.ID
				ref.Quote = chunk.Text
			}
			refs = append(refs, ref)
		}
		return dedupeEvidenceRefs(refs)
	}

	refs := make([]research.EvidenceRef, 0, len(finding.EvidenceRefs))
	for _, ref := range finding.EvidenceRefs {
		ref.SourceID = strings.TrimSpace(ref.SourceID)
		ref.ChunkID = strings.TrimSpace(ref.ChunkID)
		ref.Quote = inlineText(ref.Quote)
		if ref.SourceID == "" {
			continue
		}
		if ref.ChunkID == "" {
			if chunk, ok := index.firstChunkBySource[ref.SourceID]; ok {
				ref.ChunkID = chunk.ID
				if ref.Quote == "" {
					ref.Quote = chunk.Text
				}
			}
		}
		if ref.Quote == "" {
			if chunk, ok := index.chunkByID[ref.ChunkID]; ok {
				ref.Quote = chunk.Text
			}
		}
		refs = append(refs, ref)
	}
	return dedupeEvidenceRefs(refs)
}

// evidenceRefsForClaimEvidence 返回 final answer claim 的可渲染证据。
func evidenceRefsForClaimEvidence(evidence research.ClaimEvidence, index evidenceIndex) []research.EvidenceRef {
	if len(evidence.EvidenceRefs) == 0 {
		finding := research.Finding{SourceIDs: evidence.SourceIDs}
		return evidenceRefsForFinding(finding, index)
	}
	finding := research.Finding{EvidenceRefs: evidence.EvidenceRefs}
	return evidenceRefsForFinding(finding, index)
}

// renderEvidenceRef 将单个 evidence ref 渲染为短 quote 加 source/chunk 信息。
func renderEvidenceRef(ref research.EvidenceRef, index evidenceIndex) string {
	ref.SourceID = strings.TrimSpace(ref.SourceID)
	ref.ChunkID = strings.TrimSpace(ref.ChunkID)
	if ref.SourceID == "" {
		return ""
	}

	quote := truncateInline(inlineText(ref.Quote), maxRenderedEvidenceQuoteChars)
	var parts []string
	if label := sourceLabel(ref.SourceID, index); label != "" {
		parts = append(parts, "Source: "+label)
	}
	if ref.ChunkID != "" {
		parts = append(parts, "chunk `"+ref.ChunkID+"`")
	}

	if quote == "" {
		return strings.Join(parts, ", ")
	}

	quote = strings.ReplaceAll(quote, `"`, `'`)
	if len(parts) == 0 {
		return `"` + quote + `"`
	}
	return `"` + quote + `" (` + strings.Join(parts, ", ") + `)`
}

// sourceLabel 拼接 source id、标题和 URL，作为 evidence quote 后的可核对来源。
func sourceLabel(sourceID string, index evidenceIndex) string {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return ""
	}

	source, hasSource := index.sourceByID[sourceID]
	document, hasDocument := index.documentBySourceID[sourceID]

	title := ""
	url := ""
	if hasSource {
		title = inlineText(source.Title)
		url = strings.TrimSpace(source.URL)
	}
	if title == "" && hasDocument {
		title = inlineText(document.Title)
	}
	if url == "" && hasDocument {
		url = strings.TrimSpace(document.URL)
	}

	label := sourceID
	if title != "" {
		label += " " + title
	}
	if url != "" {
		label += " - " + url
	}
	return label
}

// formatCitationIDs 渲染 finding claim 后面的简短 source id 列表。
func formatCitationIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "[" + strings.Join(ids, ", ") + "]"
}

// dedupeEvidenceRefs 按 source_id + chunk_id 去重。
func dedupeEvidenceRefs(refs []research.EvidenceRef) []research.EvidenceRef {
	seen := make(map[string]struct{}, len(refs))
	out := make([]research.EvidenceRef, 0, len(refs))
	for _, ref := range refs {
		ref.SourceID = strings.TrimSpace(ref.SourceID)
		ref.ChunkID = strings.TrimSpace(ref.ChunkID)
		if ref.SourceID == "" {
			continue
		}
		key := ref.SourceID + "\x00" + ref.ChunkID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

// dedupeInlineStrings 清理并去重行内字符串。
func dedupeInlineStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// inlineText 把多行文本压成单行，避免破坏 Markdown 列表结构。
func inlineText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// truncateInline 按 rune 截断行内文本。
func truncateInline(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
