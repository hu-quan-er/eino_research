package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// SectionSynthesizer 把一个 section 下的 todo 结果归纳为结构化 SectionAnswer。
//
// 它位于 todo 调度与 FinalSynthesizer 之间，使 Final 输入从"全部 todo"压缩为
// "M 个 section answer"，并为每个 section 提供独立失败隔离。
type SectionSynthesizer interface {
	SynthesizeSection(ctx context.Context, in SectionSynthesisInput) (SectionAnswer, error)
}

// SectionSynthesisInput 是单个 section 归纳的输入。
type SectionSynthesisInput struct {
	// Question 是用户原始问题。
	Question string `json:"question"`
	// Objective 是 plan.Objective。
	Objective string `json:"objective"`
	// Section 是当前 section 定义。
	Section ResearchSection `json:"section"`
	// Todos 是该 section 下的 todo 执行结果。
	Todos []TodoExecution `json:"todos"`
	// Documents 是该 section 下各 todo Documents 去重合并后的可引用正文。
	Documents []SourceDocument `json:"documents,omitempty"`
}

// SectionAnswer 是单个 section 的结构化归纳产物。
type SectionAnswer struct {
	// SectionID 是对应 section 的稳定 ID。
	SectionID string `json:"section_id"`
	// Title 是 section 标题。
	Title string `json:"title"`
	// Summary 是该 section 的综合结论。
	Summary string `json:"summary"`
	// KeyFindings 是该 section 的核心发现，内联标注 source id，如 "X 支持 Y [todo_1_src_1]"。
	KeyFindings []string `json:"key_findings"`
	// Limitations 是该 section 的证据缺口或不确定性。
	Limitations []string `json:"limitations"`
}

// AgentSectionSynthesizer 使用模型把单个 section 归纳为 SectionAnswer。
type AgentSectionSynthesizer struct {
	model model.BaseChatModel
}

// NewAgentSectionSynthesizer 创建默认 section 合成器。
func NewAgentSectionSynthesizer(m model.BaseChatModel) *AgentSectionSynthesizer {
	return &AgentSectionSynthesizer{model: m}
}

// SynthesizeSection 调用模型归纳单个 section；输出非法或空 answer 时返回错误，
// 由上层 orchestration 走确定性兜底。
func (s *AgentSectionSynthesizer) SynthesizeSection(ctx context.Context, in SectionSynthesisInput) (SectionAnswer, error) {
	if s == nil || isNilDependency(s.model) {
		return SectionAnswer{}, fmt.Errorf("section synthesizer model is nil")
	}

	b, err := json.Marshal(buildSectionSynthesisContext(in))
	if err != nil {
		return SectionAnswer{}, fmt.Errorf("marshal section synthesis input: %w", err)
	}

	resp, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(`You are the section synthesis agent for a deep research workflow.

Summarize ONLY the provided section's todo results and documents. Write in the same language as the user's question. Do not invent facts. Cite source-backed claims inline with source IDs like [todo_1_src_1]. Move unsupported or weakly supported statements to limitations.

Return only one JSON object matching:
{
  "section_id": string,
  "title": string,
  "summary": string,
  "key_findings": [string],
  "limitations": [string]
}`),
		schema.UserMessage(string(b)),
	})
	if err != nil {
		return SectionAnswer{}, err
	}
	if resp == nil {
		return SectionAnswer{}, fmt.Errorf("section model response is nil")
	}

	answer, err := parseSectionAnswer(strings.TrimSpace(resp.Content))
	if err != nil {
		return SectionAnswer{}, err
	}
	answer.SectionID = in.Section.ID
	answer.Title = sectionTitleOrID(in.Section)
	return normalizeSectionAnswer(answer), nil
}

// parseSectionAnswer 解析模型返回的 SectionAnswer，并拒绝空 answer。
func parseSectionAnswer(content string) (SectionAnswer, error) {
	if strings.TrimSpace(content) == "" {
		return SectionAnswer{}, fmt.Errorf("section answer output is empty")
	}
	var answer SectionAnswer
	if err := json.Unmarshal([]byte(content), &answer); err != nil {
		return SectionAnswer{}, fmt.Errorf("invalid SectionAnswer JSON: %w", err)
	}
	if isEmptySectionAnswer(answer) {
		return SectionAnswer{}, fmt.Errorf("SectionAnswer JSON contains no answer fields")
	}
	return answer, nil
}

// normalizeSectionAnswer 去除空白与空项，保证下游消费稳定。
func normalizeSectionAnswer(answer SectionAnswer) SectionAnswer {
	answer.Summary = strings.TrimSpace(answer.Summary)
	answer.KeyFindings = trimNonEmptyStrings(answer.KeyFindings)
	answer.Limitations = trimNonEmptyStrings(answer.Limitations)
	return answer
}

// isEmptySectionAnswer 判断 answer 是否没有任何可展示内容。
func isEmptySectionAnswer(answer SectionAnswer) bool {
	return strings.TrimSpace(answer.Summary) == "" &&
		len(trimNonEmptyStrings(answer.KeyFindings)) == 0 &&
		len(trimNonEmptyStrings(answer.Limitations)) == 0
}

// sectionTitleOrID 返回 section 标题，缺省时回退到 ID。
func sectionTitleOrID(section ResearchSection) string {
	if title := strings.TrimSpace(section.Title); title != "" {
		return title
	}
	if id := strings.TrimSpace(section.ID); id != "" {
		return id
	}
	return "Untitled Section"
}

// buildSectionSynthesisContext 压缩单 section 输入，只保留归纳所需的 todo 字段。
func buildSectionSynthesisContext(in SectionSynthesisInput) sectionSynthesisContext {
	todos := make([]sectionTodoContext, 0, len(in.Todos))
	for _, todo := range in.Todos {
		todos = append(todos, sectionTodoContext{
			ID:       todo.Todo.ID,
			Title:    todo.Todo.Title,
			Question: todo.Todo.Question,
			Status:   todo.Status,
			Summary:  todo.Summary,
			Findings: todo.Findings,
			Gaps:     todo.Gaps,
			Error:    todo.Error,
		})
	}
	return sectionSynthesisContext{
		Question:    in.Question,
		Objective:   in.Objective,
		SectionID:   in.Section.ID,
		Title:       in.Section.Title,
		Description: in.Section.Description,
		Todos:       todos,
		Documents:   in.Documents,
	}
}

type sectionSynthesisContext struct {
	Question    string               `json:"question"`
	Objective   string               `json:"objective"`
	SectionID   string               `json:"section_id"`
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Todos       []sectionTodoContext `json:"todos"`
	Documents   []SourceDocument     `json:"documents,omitempty"`
}

type sectionTodoContext struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Question string     `json:"question"`
	Status   TodoStatus `json:"status"`
	Summary  string     `json:"summary"`
	Findings []Finding  `json:"findings,omitempty"`
	Gaps     []string   `json:"gaps,omitempty"`
	Error    string     `json:"error,omitempty"`
}
