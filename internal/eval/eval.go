package eval

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
	"gopkg.in/yaml.v3"
)

// Suite 是一组可重复运行的 research 评测用例。
type Suite struct {
	// Cases 是评测用例集合；顺序会按 YAML 中的声明顺序保留。
	Cases []Case `yaml:"cases" json:"cases"`
}

// Case 描述一个固定评测问题和规则化期望。
type Case struct {
	// ID 是用例稳定标识，用于报告、过滤和失败定位。
	ID string `yaml:"id" json:"id"`
	// Question 是传给 research runner 的原始问题。
	Question string `yaml:"question" json:"question"`
	// RequiredClaims 是最终答案中必须出现的文本片段。
	RequiredClaims []string `yaml:"required_claims,omitempty" json:"required_claims,omitempty"`
	// ForbiddenClaims 是最终答案中不应出现的文本片段。
	ForbiddenClaims []string `yaml:"forbidden_claims,omitempty" json:"forbidden_claims,omitempty"`
	// RequiredSourceHints 是 sources 元数据中必须出现的域名、标题或关键词提示。
	RequiredSourceHints []string `yaml:"required_source_hints,omitempty" json:"required_source_hints,omitempty"`
	// MinCitationCoverage 要求 Answer.Evidence 中 supported claim 的最低比例。
	MinCitationCoverage float64 `yaml:"min_citation_coverage,omitempty" json:"min_citation_coverage,omitempty"`
	// MaxUnsupportedClaims 允许的 unsupported claim 数量上限。
	MaxUnsupportedClaims int `yaml:"max_unsupported_claims,omitempty" json:"max_unsupported_claims,omitempty"`
	// MinSourceDiversity 要求最终 sources 至少覆盖多少个不同 host。
	MinSourceDiversity int `yaml:"min_source_diversity,omitempty" json:"min_source_diversity,omitempty"`
}

// Metrics 是不依赖模型 judge 的基础评测指标。
type Metrics struct {
	// CitationCoverage 是 supported evidence 数 / evidence 总数；没有 evidence 时为 0。
	CitationCoverage float64 `json:"citation_coverage"`
	// UnsupportedClaimCount 是 Answer.Evidence 中 Supported=false 的条数。
	UnsupportedClaimCount int `json:"unsupported_claim_count"`
	// SourceDiversity 是最终 sources 中不同 hostname 的数量。
	SourceDiversity int `json:"source_diversity"`
}

// Evaluation 是单个 case 对某次 ResearchResult 的评测结果。
type Evaluation struct {
	// CaseID 对应 Case.ID。
	CaseID string `json:"case_id"`
	// Passed 表示所有规则检查是否通过。
	Passed bool `json:"passed"`
	// Metrics 是本次结果的基础量化指标。
	Metrics Metrics `json:"metrics"`
	// Issues 记录每条未通过的规则，便于 CI 或人工定位。
	Issues []string `json:"issues,omitempty"`
}

// LoadSuite 从 YAML 文件读取评测用例，并启用 KnownFields 防止字段拼写错误。
func LoadSuite(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("read eval suite %s: %w", path, err)
	}
	var suite Suite
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode eval suite %s: %w", path, err)
	}
	if err := suite.Validate(); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

// Validate 校验评测用例的基本字段。
func (s Suite) Validate() error {
	seen := make(map[string]struct{}, len(s.Cases))
	for i, c := range s.Cases {
		id := strings.TrimSpace(c.ID)
		if id == "" {
			return fmt.Errorf("eval case %d id is required", i)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("eval case id %q is duplicated", id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(c.Question) == "" {
			return fmt.Errorf("eval case %s question is required", id)
		}
	}
	return nil
}

// EvaluateResult 使用规则指标评估一次 ResearchResult。
func EvaluateResult(result research.ResearchResult, c Case) Evaluation {
	evaluation := Evaluation{
		CaseID:  c.ID,
		Metrics: computeMetrics(result),
	}
	answerText := normalizedText(strings.Join([]string{
		result.Answer.Markdown,
		result.Answer.Summary,
		strings.Join(result.Answer.KeyFindings, "\n"),
		strings.Join(result.Answer.Limitations, "\n"),
	}, "\n"))
	sourceText := normalizedText(joinSources(result.Sources))

	// Required/forbidden claims 采用大小写和空白无关的子串匹配，保持评测层轻量稳定。
	for _, claim := range c.RequiredClaims {
		if !strings.Contains(answerText, normalizedText(claim)) {
			evaluation.Issues = append(evaluation.Issues, "missing required claim: "+claim)
		}
	}
	for _, claim := range c.ForbiddenClaims {
		if strings.Contains(answerText, normalizedText(claim)) {
			evaluation.Issues = append(evaluation.Issues, "forbidden claim present: "+claim)
		}
	}
	for _, hint := range c.RequiredSourceHints {
		if !strings.Contains(sourceText, normalizedText(hint)) {
			evaluation.Issues = append(evaluation.Issues, "missing required source hint: "+hint)
		}
	}
	if c.MinCitationCoverage > 0 && evaluation.Metrics.CitationCoverage < c.MinCitationCoverage {
		evaluation.Issues = append(evaluation.Issues, fmt.Sprintf("citation coverage %.2f below %.2f", evaluation.Metrics.CitationCoverage, c.MinCitationCoverage))
	}
	if evaluation.Metrics.UnsupportedClaimCount > c.MaxUnsupportedClaims {
		evaluation.Issues = append(evaluation.Issues, fmt.Sprintf("unsupported claims %d above %d", evaluation.Metrics.UnsupportedClaimCount, c.MaxUnsupportedClaims))
	}
	if c.MinSourceDiversity > 0 && evaluation.Metrics.SourceDiversity < c.MinSourceDiversity {
		evaluation.Issues = append(evaluation.Issues, fmt.Sprintf("source diversity %d below %d", evaluation.Metrics.SourceDiversity, c.MinSourceDiversity))
	}
	evaluation.Passed = len(evaluation.Issues) == 0
	return evaluation
}

// computeMetrics 从最终 Answer.Evidence 和 Sources 中计算非模型指标。
func computeMetrics(result research.ResearchResult) Metrics {
	totalClaims := len(result.Answer.Evidence)
	supportedClaims := 0
	unsupportedClaims := 0
	for _, evidence := range result.Answer.Evidence {
		if evidence.Supported {
			supportedClaims++
		} else {
			unsupportedClaims++
		}
	}
	coverage := 0.0
	if totalClaims > 0 {
		coverage = float64(supportedClaims) / float64(totalClaims)
	}
	return Metrics{
		CitationCoverage:      coverage,
		UnsupportedClaimCount: unsupportedClaims,
		SourceDiversity:       countSourceHosts(result.Sources),
	}
}

// countSourceHosts 统计 sources 中不同 hostname 数量，用于衡量来源多样性。
func countSourceHosts(sources []search.Source) int {
	hosts := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		host := sourceHost(source.URL)
		if host == "" {
			continue
		}
		hosts[host] = struct{}{}
	}
	return len(hosts)
}

// joinSources 把 source 元数据合并成一个字符串，供 RequiredSourceHints 做宽松匹配。
func joinSources(sources []search.Source) string {
	parts := make([]string, 0, len(sources)*3)
	for _, source := range sources {
		parts = append(parts, source.ID, source.Title, source.URL, source.Snippet, source.Provider, source.Query)
	}
	return strings.Join(parts, "\n")
}

// normalizedText 把文本归一成小写、单空格形式，避免格式差异影响规则判断。
func normalizedText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// sourceHost 从 URL 中抽取小写 hostname；非法 URL 返回空字符串。
func sourceHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
