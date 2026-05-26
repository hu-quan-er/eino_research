package research

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/hu-quan-er/eino_research/internal/search"
)

const (
	defaultMaxEvidenceClaims = 12
	defaultMinEvidenceScore  = 2
)

var sourceIDCitationPattern = regexp.MustCompile(`\[([A-Za-z0-9_.-]+(?:\s*,\s*[A-Za-z0-9_.-]+)*)\]`)

// EvidenceBinder 负责把最终 Answer 中的关键 claim 绑定回 source/document/chunk。
type EvidenceBinder interface {
	BindEvidence(ctx context.Context, in EvidenceBindingInput) (Answer, error)
}

// EvidenceBindingInput 是最终答案证据绑定阶段需要的上下文。
type EvidenceBindingInput struct {
	// Question 是用户原始问题，后续模型化 binder 可以用它判断 claim 是否回答了问题。
	Question string
	// Answer 是 FinalSynthesizer 生成的最终答案。
	Answer Answer
	// Sources 是最终去重后的来源列表。
	Sources []search.Source
	// Documents 是可引用的正文切片。
	Documents []SourceDocument
	// TodoExecutions 保留 todo-level findings，作为 documents 不足时的补充证据候选。
	TodoExecutions []TodoExecution
}

// RuleBasedEvidenceBinder 使用确定性规则做第一版最终答案证据绑定。
//
// 规则优先消费 final answer 中已经出现的 source id；没有显式 source id 时，再用简单文本
// overlap 从 SourceDocument.Chunks 和 todo findings 中选择候选证据。
type RuleBasedEvidenceBinder struct {
	// MaxClaims 限制最多绑定多少条最终 claim，避免报告过长。
	MaxClaims int
	// MinScore 是无显式 source id 时文本匹配的最低分。
	MinScore int
}

// BindEvidence 返回带 Answer.Evidence 的答案，并把未找到证据的 claim 追加到 limitations。
func (b RuleBasedEvidenceBinder) BindEvidence(ctx context.Context, in EvidenceBindingInput) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return in.Answer, err
	}

	maxClaims := b.MaxClaims
	if maxClaims <= 0 {
		maxClaims = defaultMaxEvidenceClaims
	}
	minScore := b.MinScore
	if minScore <= 0 {
		minScore = defaultMinEvidenceScore
	}

	candidates := buildEvidenceCandidates(in.Sources, in.Documents, in.TodoExecutions)
	sourceIDs := candidateSourceIDs(candidates, in.Sources, in.Documents)
	claims := extractAnswerClaims(in.Answer, maxClaims)
	if len(claims) == 0 {
		return in.Answer, nil
	}

	answer := in.Answer
	answer.Evidence = make([]ClaimEvidence, 0, len(claims))
	unsupported := make([]string, 0)
	for _, claim := range claims {
		evidence := bindClaimEvidence(claim, candidates, sourceIDs, minScore)
		answer.Evidence = append(answer.Evidence, evidence)
		if !evidence.Supported {
			unsupported = append(unsupported, fmt.Sprintf("Unsupported final claim: %s", evidence.Claim))
		}
	}
	answer.Limitations = dedupeStrings(append(answer.Limitations, unsupported...))
	return answer, nil
}

// bindFinalEvidence 调用配置中的 evidence binder，并在失败时保留原答案。
func (r *Runner) bindFinalEvidence(ctx context.Context, in EvidenceBindingInput) Answer {
	binder := r.cfg.EvidenceBinder
	if binder == nil {
		binder = RuleBasedEvidenceBinder{}
	}

	answer, err := binder.BindEvidence(ctx, in)
	if err != nil {
		in.Answer.Limitations = dedupeStrings(append(in.Answer.Limitations, "Evidence binding skipped: "+err.Error()))
		return in.Answer
	}
	return answer
}

type answerClaimCandidate struct {
	Raw   string
	Claim string
}

type evidenceCandidate struct {
	SourceID string
	ChunkID  string
	Quote    string
	Text     string
}

func extractAnswerClaims(answer Answer, limit int) []answerClaimCandidate {
	rawClaims := make([]string, 0, len(answer.KeyFindings))
	rawClaims = append(rawClaims, answer.KeyFindings...)
	if len(rawClaims) == 0 {
		rawClaims = extractMarkdownClaimLines(answer.Markdown, limit)
	}
	if len(rawClaims) == 0 && strings.TrimSpace(answer.Summary) != "" {
		rawClaims = append(rawClaims, answer.Summary)
	}

	out := make([]answerClaimCandidate, 0, len(rawClaims))
	seen := make(map[string]struct{}, len(rawClaims))
	for _, raw := range rawClaims {
		raw = strings.TrimSpace(raw)
		claim := stripCitationText(raw)
		if claim == "" {
			continue
		}
		key := strings.ToLower(claim)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, answerClaimCandidate{Raw: raw, Claim: claim})
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	return out
}

func extractMarkdownClaimLines(markdown string, limit int) []string {
	lines := strings.Split(markdown, "\n")
	claims := make([]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		line = trimNumberedListPrefix(line)
		if len([]rune(line)) < 12 {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "source:") || strings.HasPrefix(lower, "evidence:") {
			continue
		}
		claims = append(claims, line)
		if limit > 0 && len(claims) >= limit {
			return claims
		}
	}
	return claims
}

func trimNumberedListPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) {
		return line
	}
	if line[i] != '.' && line[i] != ')' {
		return line
	}
	return strings.TrimSpace(line[i+1:])
}

func bindClaimEvidence(claim answerClaimCandidate, candidates []evidenceCandidate, knownSourceIDs map[string]struct{}, minScore int) ClaimEvidence {
	explicitSourceIDs := extractKnownSourceIDs(claim.Raw, knownSourceIDs)
	refs := refsForSourceIDs(explicitSourceIDs, candidates)
	if len(refs) > 0 {
		return ClaimEvidence{
			Claim:        claim.Claim,
			SourceIDs:    sourceIDsFromEvidenceRefs(refs),
			EvidenceRefs: refs,
			Supported:    true,
			Reason:       "matched explicit source id in final answer",
		}
	}

	ref, score := bestCandidateForClaim(claim.Claim, candidates)
	if score >= minScore {
		return ClaimEvidence{
			Claim:        claim.Claim,
			SourceIDs:    []string{ref.SourceID},
			EvidenceRefs: []EvidenceRef{ref},
			Supported:    true,
			Reason:       fmt.Sprintf("matched source text with score %d", score),
		}
	}

	return ClaimEvidence{
		Claim:     claim.Claim,
		Supported: false,
		Reason:    "no matching source id or sufficiently similar evidence chunk",
	}
}

func buildEvidenceCandidates(sources []search.Source, documents []SourceDocument, executions []TodoExecution) []evidenceCandidate {
	candidates := make([]evidenceCandidate, 0)
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			if strings.TrimSpace(chunk.SourceID) == "" {
				continue
			}
			candidates = append(candidates, evidenceCandidate{
				SourceID: chunk.SourceID,
				ChunkID:  chunk.ID,
				Quote:    truncateText(chunk.Text, defaultEvidenceQuoteChars),
				Text:     chunk.Text,
			})
		}
	}
	for _, source := range sources {
		text := sourceDocumentText(source)
		if strings.TrimSpace(source.ID) == "" || text == "" {
			continue
		}
		candidates = append(candidates, evidenceCandidate{
			SourceID: source.ID,
			Quote:    truncateText(text, defaultEvidenceQuoteChars),
			Text:     text,
		})
	}
	for _, execution := range executions {
		for _, finding := range execution.Findings {
			text := strings.TrimSpace(finding.Claim + " " + finding.Rationale)
			if text == "" {
				continue
			}
			for _, ref := range finding.EvidenceRefs {
				if strings.TrimSpace(ref.SourceID) == "" {
					continue
				}
				candidates = append(candidates, evidenceCandidate{
					SourceID: ref.SourceID,
					ChunkID:  ref.ChunkID,
					Quote:    ref.Quote,
					Text:     text + " " + ref.Quote,
				})
			}
		}
	}
	return candidates
}

func candidateSourceIDs(candidates []evidenceCandidate, sources []search.Source, documents []SourceDocument) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, candidate := range candidates {
		if id := strings.TrimSpace(candidate.SourceID); id != "" {
			ids[id] = struct{}{}
		}
	}
	for _, source := range sources {
		if id := strings.TrimSpace(source.ID); id != "" {
			ids[id] = struct{}{}
		}
	}
	for _, document := range documents {
		if id := strings.TrimSpace(document.SourceID); id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func extractKnownSourceIDs(text string, knownSourceIDs map[string]struct{}) []string {
	matches := sourceIDCitationPattern.FindAllStringSubmatch(text, -1)
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		for _, id := range strings.Split(match[1], ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := knownSourceIDs[id]; ok {
				ids = append(ids, id)
			}
		}
	}
	return dedupeStrings(ids)
}

func refsForSourceIDs(sourceIDs []string, candidates []evidenceCandidate) []EvidenceRef {
	if len(sourceIDs) == 0 {
		return nil
	}
	refs := make([]EvidenceRef, 0, len(sourceIDs))
	seenSourceID := make(map[string]struct{}, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		if _, ok := seenSourceID[sourceID]; ok {
			continue
		}
		seenSourceID[sourceID] = struct{}{}
		ref := EvidenceRef{SourceID: sourceID}
		for _, candidate := range candidates {
			if candidate.SourceID != sourceID {
				continue
			}
			ref.ChunkID = candidate.ChunkID
			ref.Quote = candidate.Quote
			break
		}
		refs = append(refs, ref)
	}
	return refs
}

func bestCandidateForClaim(claim string, candidates []evidenceCandidate) (EvidenceRef, int) {
	tokens := evidenceTokens(claim)
	if len(tokens) == 0 {
		return EvidenceRef{}, 0
	}

	bestScore := 0
	var best evidenceCandidate
	for _, candidate := range candidates {
		score := scoreEvidenceCandidate(tokens, candidate.Text)
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	if bestScore == 0 {
		return EvidenceRef{}, 0
	}
	return EvidenceRef{
		SourceID: best.SourceID,
		ChunkID:  best.ChunkID,
		Quote:    best.Quote,
	}, bestScore
}

func scoreEvidenceCandidate(tokens []string, text string) int {
	text = strings.ToLower(text)
	score := 0
	for _, token := range tokens {
		if strings.Contains(text, token) {
			score++
		}
	}
	return score
}

func evidenceTokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(stripCitationText(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	tokens := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 3 || isEvidenceStopword(field) {
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

func stripCitationText(text string) string {
	text = strings.Join(strings.Fields(sourceIDCitationPattern.ReplaceAllString(text, "")), " ")
	for _, punct := range []string{".", ",", ";", ":", "?", "!"} {
		text = strings.ReplaceAll(text, " "+punct, punct)
	}
	return text
}

func isEvidenceStopword(token string) bool {
	switch token {
	case "the", "and", "for", "with", "from", "that", "this", "into", "onto", "when", "where", "what", "why", "how", "can", "should", "would", "could", "use", "used", "using", "has", "have", "are", "was", "were", "its", "their", "there":
		return true
	default:
		return false
	}
}
