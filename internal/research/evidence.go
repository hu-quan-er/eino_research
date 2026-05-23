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

func enrichResearcherEvidence(results []ResearcherResult, documents []SourceDocument) []ResearcherResult {
	if len(results) == 0 {
		return results
	}

	out := make([]ResearcherResult, len(results))
	for i, result := range results {
		result.Findings = enrichFindingsEvidence(result.Findings, documents)
		result.Documents = buildSourceDocuments(result.Sources, defaultSourceChunkChars)
		out[i] = result
	}
	return out
}

func enrichFindingsEvidence(findings []Finding, documents []SourceDocument) []Finding {
	if len(findings) == 0 {
		return findings
	}

	chunksBySource := firstChunkBySourceID(documents)
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		finding.SourceIDs = dedupeStrings(append(finding.SourceIDs, sourceIDsFromEvidenceRefs(finding.EvidenceRefs)...))
		if len(finding.EvidenceRefs) == 0 {
			finding.EvidenceRefs = evidenceRefsFromSourceIDs(finding.SourceIDs, chunksBySource)
		} else {
			finding.EvidenceRefs = normalizeEvidenceRefs(finding.EvidenceRefs, chunksBySource)
		}
		out[i] = finding
	}
	return out
}

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

func sourceIDsFromEvidenceRefs(refs []EvidenceRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if sourceID := strings.TrimSpace(ref.SourceID); sourceID != "" {
			out = append(out, sourceID)
		}
	}
	return out
}

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

func mergeSourceDocuments(primary, fallback []SourceDocument) []SourceDocument {
	if len(primary) == 0 {
		return dedupeSourceDocuments(fallback)
	}
	if len(fallback) == 0 {
		return dedupeSourceDocuments(primary)
	}
	return dedupeSourceDocuments(append(primary, fallback...))
}

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
