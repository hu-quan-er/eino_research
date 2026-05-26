package search

import (
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// RankSources 使用确定性规则给搜索结果打分并稳定排序。
//
// 第一版不依赖 embedding/reranker，优先提升来源权威性、可读性和 query 相关性。
func RankSources(query string, sources []Source) []Source {
	out := make([]Source, len(sources))
	copy(out, sources)
	for i := range out {
		score, reason := scoreSource(query, out[i])
		out[i].RankScore = score
		out[i].RankReason = reason
		if strings.TrimSpace(out[i].Query) == "" {
			out[i].Query = query
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].RankScore > out[j].RankScore
	})
	return out
}

func scoreSource(query string, source Source) (float64, string) {
	score := 1.0
	reasons := []string{"base"}

	parsed, _ := url.Parse(source.URL)
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.Path)

	if parsed.Scheme == "https" {
		score += 0.2
		reasons = append(reasons, "https")
	}
	if strings.TrimSpace(source.Snippet) != "" {
		score += 0.4
		reasons = append(reasons, "snippet")
	}
	if isOfficialLikeSource(host, path, source) {
		score += 1.5
		reasons = append(reasons, "official_or_docs")
	}
	if isScholarlySource(host) {
		score += 1.4
		reasons = append(reasons, "scholarly")
	}
	if isStandardsOrPublicSource(host) {
		score += 1.0
		reasons = append(reasons, "standards_or_public")
	}
	if host == "github.com" || strings.HasSuffix(host, ".github.com") {
		score += 0.8
		reasons = append(reasons, "github")
	}

	overlap := tokenOverlap(query, source.Title+" "+source.Snippet+" "+source.URL)
	if overlap > 0 {
		score += float64(overlap) * 0.25
		reasons = append(reasons, "query_overlap")
	}

	return score, strings.Join(reasons, ",")
}

func isOfficialLikeSource(host, path string, source Source) bool {
	text := strings.ToLower(source.Title + " " + source.Snippet + " " + source.URL)
	return strings.HasPrefix(host, "docs.") ||
		strings.Contains(host, ".docs.") ||
		strings.Contains(path, "/docs") ||
		strings.Contains(path, "/documentation") ||
		strings.Contains(text, "official documentation") ||
		strings.Contains(text, "official docs") ||
		strings.Contains(text, "developer documentation")
}

func isScholarlySource(host string) bool {
	for _, domain := range []string{
		"arxiv.org",
		"doi.org",
		"acm.org",
		"ieee.org",
		"nature.com",
		"science.org",
		"springer.com",
		"sciencedirect.com",
		"semanticscholar.org",
	} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func isStandardsOrPublicSource(host string) bool {
	if strings.HasSuffix(host, ".gov") || strings.HasSuffix(host, ".edu") {
		return true
	}
	for _, domain := range []string{
		"w3.org",
		"ietf.org",
		"iso.org",
		"nist.gov",
	} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func tokenOverlap(query, text string) int {
	queryTokens := rankTokens(query)
	if len(queryTokens) == 0 {
		return 0
	}
	textTokens := make(map[string]struct{})
	for _, token := range rankTokens(text) {
		textTokens[token] = struct{}{}
	}
	overlap := 0
	for _, token := range queryTokens {
		if _, ok := textTokens[token]; ok {
			overlap++
		}
	}
	return overlap
}

func rankTokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	tokens := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 3 || isRankStopword(field) {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		tokens = append(tokens, field)
	}
	return tokens
}

func isRankStopword(token string) bool {
	switch token {
	case "the", "and", "for", "with", "from", "that", "this", "into", "what", "when", "where", "why", "how", "can", "should", "would", "could":
		return true
	default:
		return false
	}
}
