package research

import (
	"fmt"
	"strings"

	"github.com/hu-quan-er/eino_research/internal/search"
)

const (
	defaultSourceChunkChars   = 900
	defaultEvidenceQuoteChars = 240
)

// buildSourceDocuments 从搜索结果构造最小 SourceDocument。
//
// 当还没有 web_fetch 正文时，snippet/title 是可用的兜底证据来源；后续如果抓取到完整页面，
// buildFetchedPageDocuments 会提供更高质量的 document。
func buildSourceDocuments(sources []search.Source, maxChunkChars int) []SourceDocument {
	if maxChunkChars <= 0 {
		maxChunkChars = defaultSourceChunkChars
	}

	documents := make([]SourceDocument, 0, len(sources))
	for _, source := range sources {
		sourceID := strings.TrimSpace(source.ID)
		if sourceID == "" || strings.TrimSpace(source.URL) == "" {
			continue
		}
		text := sourceDocumentText(source)
		if text == "" {
			continue
		}

		documentID := sourceID + "_doc"
		documents = append(documents, SourceDocument{
			ID:       documentID,
			SourceID: sourceID,
			Title:    source.Title,
			URL:      source.URL,
			Provider: source.Provider,
			Query:    source.Query,
			Chunks:   chunkSourceText(sourceID, documentID, text, maxChunkChars),
		})
	}
	return documents
}

// buildFetchedPageDocuments 将 web_fetch 成功读取的页面转换为 SourceDocument。
//
// 如果页面 URL 能匹配已有 Source，会复用该 SourceID；否则分配 fetched_N，保证孤立抓取结果
// 也能进入 documents。
func buildFetchedPageDocuments(pages []FetchedPage, sources []search.Source, maxChunkChars int) []SourceDocument {
	if maxChunkChars <= 0 {
		maxChunkChars = defaultSourceChunkChars
	}

	sourceByURL := make(map[string]search.Source, len(sources))
	for _, source := range sources {
		if url := strings.TrimSpace(source.URL); url != "" {
			// 用 URL 建索引是为了把 web_fetch 的页面重新挂回 search source。
			sourceByURL[url] = source
		}
	}

	documents := make([]SourceDocument, 0, len(pages))
	seenURL := make(map[string]struct{}, len(pages))
	nextFetchedID := 1
	for _, page := range pages {
		page.URL = strings.TrimSpace(page.URL)
		page.Text = strings.TrimSpace(page.Text)
		if page.URL == "" || page.Text == "" {
			continue
		}
		if _, ok := seenURL[page.URL]; ok {
			continue
		}
		seenURL[page.URL] = struct{}{}

		source, ok := sourceByURL[page.URL]
		sourceID := strings.TrimSpace(source.ID)
		if !ok || sourceID == "" {
			// fetch 可能读取了非搜索结果中的链接，这类孤立页面也需要可引用 ID。
			sourceID = fmt.Sprintf("fetched_%d", nextFetchedID)
			nextFetchedID++
		}
		title := strings.TrimSpace(page.Title)
		if title == "" {
			title = source.Title
		}
		documentID := sourceID + "_doc"
		documents = append(documents, SourceDocument{
			ID:       documentID,
			SourceID: sourceID,
			Title:    title,
			URL:      page.URL,
			Provider: source.Provider,
			Query:    source.Query,
			Chunks:   chunkSourceText(sourceID, documentID, normalizeWhitespace(page.Text), maxChunkChars),
		})
	}
	return documents
}

// sourceDocumentText 选择搜索结果中可用于构造 document 的文本，优先 snippet，其次 title。
func sourceDocumentText(source search.Source) string {
	text := strings.TrimSpace(source.Snippet)
	if text != "" {
		return normalizeWhitespace(text)
	}
	text = strings.TrimSpace(source.Title)
	if text != "" {
		return normalizeWhitespace(text)
	}
	return ""
}

// chunkSourceText 按 rune 数把正文切成可引用的 SourceChunk。
//
// 当前实现是简单定长切分；后续可升级为按段落边界和 overlap 切分。
func chunkSourceText(sourceID, documentID, text string, maxChunkChars int) []SourceChunk {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}

	chunks := make([]SourceChunk, 0, (len(runes)/maxChunkChars)+1)
	for start := 0; start < len(runes); start += maxChunkChars {
		end := start + maxChunkChars
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, SourceChunk{
			ID:         fmt.Sprintf("%s_chunk_%d", sourceID, len(chunks)+1),
			DocumentID: documentID,
			SourceID:   sourceID,
			Text:       string(runes[start:end]),
			StartChar:  start,
			EndChar:    end,
		})
	}
	return chunks
}

// enrichResearcherEvidence 为 researcher findings 补齐 evidence_refs，并合并 researcher 局部
// documents。
func enrichResearcherEvidence(results []ResearcherResult, documents []SourceDocument) []ResearcherResult {
	if len(results) == 0 {
		return results
	}

	out := make([]ResearcherResult, len(results))
	for i, result := range results {
		result.Findings = enrichFindingsEvidence(result.Findings, documents)
		result.Documents = mergeSourceDocuments(result.Documents, buildSourceDocuments(result.Sources, defaultSourceChunkChars))
		out[i] = result
	}
	return out
}

// enrichFindingsEvidence 确保 finding 同时具备 source_ids 和 evidence_refs。
//
// 这样即便模型只返回旧字段 source_ids，最终报告仍然可以展示 chunk quote。
func enrichFindingsEvidence(findings []Finding, documents []SourceDocument) []Finding {
	if len(findings) == 0 {
		return findings
	}

	chunksBySource := firstChunkBySourceID(documents)
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		// evidence_refs 里的 source_id 也要同步回填到 source_ids，保持新旧字段一致。
		finding.SourceIDs = dedupeStrings(append(finding.SourceIDs, sourceIDsFromEvidenceRefs(finding.EvidenceRefs)...))
		if len(finding.EvidenceRefs) == 0 {
			// 老模型只返回 source_ids 时，使用该 source 的首个 chunk 生成 quote 兜底。
			finding.EvidenceRefs = evidenceRefsFromSourceIDs(finding.SourceIDs, chunksBySource)
		} else {
			finding.EvidenceRefs = normalizeEvidenceRefs(finding.EvidenceRefs, chunksBySource)
		}
		out[i] = finding
	}
	return out
}

// firstChunkBySourceID 为每个 source 取第一个 chunk，作为缺失 evidence_ref 时的兜底证据。
func firstChunkBySourceID(documents []SourceDocument) map[string]SourceChunk {
	out := make(map[string]SourceChunk, len(documents))
	for _, document := range documents {
		if len(document.Chunks) == 0 {
			continue
		}
		sourceID := strings.TrimSpace(document.SourceID)
		if sourceID == "" {
			continue
		}
		if _, ok := out[sourceID]; !ok {
			out[sourceID] = document.Chunks[0]
		}
	}
	return out
}

// sourceIDsFromEvidenceRefs 从 evidence_refs 反推 source_ids，用于保持新旧 citation 字段一致。
func sourceIDsFromEvidenceRefs(refs []EvidenceRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if sourceID := strings.TrimSpace(ref.SourceID); sourceID != "" {
			out = append(out, sourceID)
		}
	}
	return out
}

// evidenceRefsFromSourceIDs 根据 source_ids 自动生成 evidence_refs。
func evidenceRefsFromSourceIDs(sourceIDs []string, chunks map[string]SourceChunk) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(sourceIDs))
	seen := make(map[string]struct{}, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			continue
		}
		if _, ok := seen[sourceID]; ok {
			continue
		}
		seen[sourceID] = struct{}{}
		ref := EvidenceRef{SourceID: sourceID}
		if chunk, ok := chunks[sourceID]; ok {
			ref.ChunkID = chunk.ID
			ref.Quote = truncateText(chunk.Text, defaultEvidenceQuoteChars)
		}
		refs = append(refs, ref)
	}
	return refs
}

// normalizeEvidenceRefs 清洗模型返回的 evidence_refs，并在缺少 chunk/quote 时尝试补齐。
func normalizeEvidenceRefs(refs []EvidenceRef, chunks map[string]SourceChunk) []EvidenceRef {
	out := make([]EvidenceRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref.SourceID = strings.TrimSpace(ref.SourceID)
		ref.ChunkID = strings.TrimSpace(ref.ChunkID)
		ref.Quote = strings.TrimSpace(ref.Quote)
		if ref.SourceID == "" {
			continue
		}
		if ref.ChunkID == "" {
			if chunk, ok := chunks[ref.SourceID]; ok {
				// 模型只给 source_id 时，用首个 chunk 补齐 chunk_id 和短 quote。
				ref.ChunkID = chunk.ID
				if ref.Quote == "" {
					ref.Quote = truncateText(chunk.Text, defaultEvidenceQuoteChars)
				}
			}
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

// mergeSourceDocuments 合并 documents，primary 优先。
//
// 执行层会把 fetched page documents 放在 primary，使完整正文优先于搜索 snippet。
func mergeSourceDocuments(primary, fallback []SourceDocument) []SourceDocument {
	if len(primary) == 0 {
		return dedupeSourceDocuments(fallback)
	}
	if len(fallback) == 0 {
		return dedupeSourceDocuments(primary)
	}
	return dedupeSourceDocuments(append(primary, fallback...))
}

// dedupeSourceDocuments 按 SourceID 去重；没有 SourceID 时按 URL 去重。
func dedupeSourceDocuments(documents []SourceDocument) []SourceDocument {
	seen := make(map[string]struct{}, len(documents))
	out := make([]SourceDocument, 0, len(documents))
	for _, document := range documents {
		key := strings.TrimSpace(document.SourceID)
		if key == "" {
			key = strings.TrimSpace(document.URL)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, document)
	}
	return out
}
