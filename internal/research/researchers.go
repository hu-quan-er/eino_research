package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

// AgentResearcher 是基于 Eino ChatModelAgent 的 researcher 实现。
//
// 每个实例绑定一个角色和 focus，执行时会通过 web_search/web_fetch 收集资料，并要求模型
// 返回 ResearcherResult JSON。
type AgentResearcher struct {
	// role 是该 agent 的稳定角色 ID。
	role string
	// focus 是该 agent 的研究侧重点，会写入 prompt。
	focus string
	// agent 是底层 Eino ChatModelAgent。
	agent adk.Agent
}

// NewAgentResearcher 创建一个可使用研究工具的子代理。
//
// tools 至少需要包含 web_search；传入 web_fetch 后，模型可以读取搜索结果中的关键 URL
// 并把正文片段写入 documents。
func NewAgentResearcher(ctx context.Context, role, focus string, m model.BaseChatModel, tools ...tool.BaseTool) (*AgentResearcher, error) {
	if strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("role is required")
	}
	if strings.TrimSpace(focus) == "" {
		return nil, fmt.Errorf("focus is required")
	}
	if isNilDependency(m) {
		return nil, fmt.Errorf("model is nil")
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("at least one research tool is required")
	}
	for i, researchTool := range tools {
		if isNilDependency(researchTool) {
			return nil, fmt.Errorf("research tool %d is nil", i)
		}
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        role,
		Description: fmt.Sprintf("Research agent focused on %s.", focus),
		Instruction: fmt.Sprintf(`You are %s. Focus on %s.

Use web_search to discover sources and web_fetch to read important source URLs when deeper evidence is needed. Return only one JSON object matching:
{
  "role": string,
  "focus": string,
  "queries": [string],
  "findings": [{"claim": string, "rationale": string, "source_ids": [string], "evidence_refs": [{"source_id": string, "quote": string}]}],
  "sources": [{"id": string, "title": string, "url": string, "snippet": string, "provider": string, "query": string}],
  "documents": [{"source_id": string, "title": string, "url": string, "chunks": [{"text": string}]}],
  "errors": [string]
}
Use source_ids and evidence_refs for every source-backed claim. When web_fetch provides important page text, include it as documents/chunks if useful. Do not wrap the JSON in markdown.`, role, focus),
		Model: m,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		},
		MaxIterations: 4,
	})
	if err != nil {
		return nil, err
	}

	return &AgentResearcher{role: role, focus: focus, agent: agent}, nil
}

// ResearcherRole 返回该 researcher 的稳定角色 ID。
func (r *AgentResearcher) ResearcherRole() string {
	if r == nil {
		return ""
	}
	return r.role
}

// ResearcherFocus 返回该 researcher 的研究侧重点。
func (r *AgentResearcher) ResearcherFocus() string {
	if r == nil {
		return ""
	}
	return r.focus
}

// Research 运行子代理并解析 ResearcherResult。
//
// 如果模型返回非 JSON 文本，这里不会直接失败，而是把文本包装成一个 finding；这样上层
// synthesis 仍有机会利用该信息，同时 Errors 不会把整个 researcher 标记为系统失败。
func (r *AgentResearcher) Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error) {
	if r == nil {
		return ResearcherResult{}, fmt.Errorf("agent researcher is nil")
	}
	if isNilDependency(r.agent) {
		return ResearcherResult{}, fmt.Errorf("researcher agent is nil")
	}

	stepPrompt := in.Step.FirstStepPrompt()
	if strings.TrimSpace(stepPrompt) == "" {
		stepPrompt = in.Step.Question
	}
	executedSteps, err := json.Marshal(in.ExecutedSteps)
	if err != nil {
		return ResearcherResult{}, fmt.Errorf("marshal executed steps: %w", err)
	}

	prompt := fmt.Sprintf(`Question:
%s

Step:
%s

Prior executed steps JSON:
%s

Assigned focus:
%s

Return only a JSON ResearcherResult object.`, in.Question, stepPrompt, string(executedSteps), r.focus)

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: r.agent})
	content, err := collectLastAssistant(runner.Run(ctx, []adk.Message{schema.UserMessage(prompt)}))
	if err != nil {
		return ResearcherResult{}, err
	}

	var result ResearcherResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// 非 JSON 输出通常是模型格式漂移，不等同于工具或系统失败；包装为 finding 后，
		// 上层 synthesizer 还能综合其中的自然语言内容。
		return ResearcherResult{
			Role:  r.role,
			Focus: r.focus,
			Findings: []Finding{{
				Claim:     strings.TrimSpace(content),
				Rationale: "model returned non-JSON researcher output",
			}},
		}, nil
	}
	if strings.TrimSpace(result.Role) == "" {
		result.Role = r.role
	}
	if strings.TrimSpace(result.Focus) == "" {
		result.Focus = r.focus
	}

	return result, nil
}

// AgentSynthesizer 使用模型把多个 researcher 输出合并为 TodoExecution。
type AgentSynthesizer struct {
	// model 是用于综合 researcher 输出的 chat model。
	model model.BaseChatModel
}

// NewAgentSynthesizer 创建 synthesizer。
func NewAgentSynthesizer(m model.BaseChatModel) *AgentSynthesizer {
	return &AgentSynthesizer{model: m}
}

// Synthesize 综合多个 researcher 输出。
//
// 如果模型没有返回合法 JSON，会回退为一个最小 TodoExecution，把 researcher 结果和 source
// 原样保留下来，避免模型格式问题导致已收集证据全部丢失。
func (s *AgentSynthesizer) Synthesize(ctx context.Context, in SynthesisInput) (TodoExecution, error) {
	if s == nil || isNilDependency(s.model) {
		return TodoExecution{}, fmt.Errorf("synthesizer model is nil")
	}

	b, err := json.Marshal(in)
	if err != nil {
		return TodoExecution{}, fmt.Errorf("marshal synthesis input: %w", err)
	}

	resp, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(`You synthesize parallel researcher outputs into one StepExecution. Return only valid JSON with fields step, researcher_results, summary, gaps, sources, and documents. Preserve source_ids, evidence_refs, and document chunks for source-backed findings.`),
		schema.UserMessage(string(b)),
	})
	if err != nil {
		return TodoExecution{}, err
	}
	if resp == nil {
		return TodoExecution{}, fmt.Errorf("model response is nil")
	}

	content := strings.TrimSpace(resp.Content)
	normalizedResults, researcherSources := normalizeResearcherSources(in.Results)
	var out TodoExecution
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		// synthesizer 偶发返回非 JSON 时，保留原文作为 summary，并继续向上游传递 researcher 证据。
		return TodoExecution{
			ResearcherResults: normalizedResults,
			Summary:           content,
			Sources:           researcherSources,
		}, nil
	}
	if len(out.ResearcherResults) == 0 {
		// 模型可能只返回 summary/sources；此时使用原始 researcher results 补齐可审计细节。
		out.ResearcherResults = normalizedResults
	} else {
		out.ResearcherResults, _ = normalizeResearcherSources(out.ResearcherResults)
	}
	// researcherSources 是原始证据主来源，synthesizer 额外返回的 sources 只作为补充。
	out.Sources = mergeSources(researcherSources, out.Sources)

	return out, nil
}

// normalizeTodoExecutionSources 是 todo 结果进入上层前的统一证据归一化入口。
//
// 它会合并 researcher sources、生成 documents、重写 finding source IDs，并为缺失的
// evidence_refs 自动补齐 quote/chunk。
func normalizeTodoExecutionSources(execution TodoExecution) TodoExecution {
	results, researcherSources := normalizeResearcherSources(execution.ResearcherResults)
	sources := mergeSources(researcherSources, execution.Sources)
	documents := mergeSourceDocuments(execution.Documents, buildSourceDocuments(sources, defaultSourceChunkChars))
	results = enrichResearcherEvidence(results, documents)
	execution.ResearcherResults = results
	execution.Sources = sources
	execution.Documents = documents
	return execution
}

// normalizeResearcherSources 合并多个 researcher 的 sources。
//
// 由于每个 researcher 都可能返回 src_1、src_2 这类局部 ID，这里会按 URL 去重并重写
// findings/evidence_refs 中的 source_id，保证综合结果里的引用指向同一套全局 source ID。
func normalizeResearcherSources(results []ResearcherResult) ([]ResearcherResult, []search.Source) {
	normalized := make([]ResearcherResult, len(results))
	urlToID := make(map[string]string)
	usedID := make(map[string]struct{})
	allSources := make([]search.Source, 0)
	nextID := 1

	for i, result := range results {
		// 每个 researcher 独立返回局部 sources，因此这里为当前 result 建一张 oldID -> newID 映射。
		idMap := make(map[string]string)
		sourceOut := make([]search.Source, 0, len(result.Sources))
		for _, source := range result.Sources {
			if source.URL == "" {
				continue
			}
			oldID := source.ID
			id, ok := urlToID[source.URL]
			if !ok {
				// 同一个 URL 第一次出现时分配全局唯一 ID。
				id = allocateSourceID(source.ID, usedID, &nextID)
				source.ID = id
				urlToID[source.URL] = id
				usedID[id] = struct{}{}
				allSources = append(allSources, source)
			} else {
				// 重复 URL 复用第一次分配的 ID，确保 finding 引用同一来源。
				source.ID = id
			}
			if strings.TrimSpace(oldID) != "" {
				idMap[oldID] = id
			}
			sourceOut = append(sourceOut, source)
		}

		result.Sources = sourceOut
		// source id 被重写后，finding/evidence_refs 也必须同步重写，否则 citation 会悬空。
		result.Findings = rewriteFindingsSourceIDs(result.Findings, idMap)
		normalized[i] = result
	}
	return normalized, allSources
}

// allocateSourceID 尽量保留候选 ID；冲突或为空时分配新的 src_N。
func allocateSourceID(candidate string, used map[string]struct{}, next *int) string {
	candidate = strings.TrimSpace(candidate)
	if candidate != "" {
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
	for {
		id := fmt.Sprintf("src_%d", *next)
		*next = *next + 1
		if _, ok := used[id]; !ok {
			return id
		}
	}
}

// rewriteFindingsSourceIDs 使用 source ID 映射重写 finding 中的 source_ids 和 evidence_refs。
func rewriteFindingsSourceIDs(findings []Finding, idMap map[string]string) []Finding {
	if len(idMap) == 0 {
		return findings
	}
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		rewritten := make([]string, 0, len(finding.SourceIDs))
		seen := make(map[string]struct{}, len(finding.SourceIDs))
		for _, sourceID := range finding.SourceIDs {
			if mapped, ok := idMap[sourceID]; ok {
				sourceID = mapped
			}
			if _, ok := seen[sourceID]; ok {
				continue
			}
			seen[sourceID] = struct{}{}
			rewritten = append(rewritten, sourceID)
		}
		finding.SourceIDs = rewritten
		finding.EvidenceRefs = rewriteEvidenceRefsSourceIDs(finding.EvidenceRefs, idMap)
		out[i] = finding
	}
	return out
}

// rewriteEvidenceRefsSourceIDs 使用 source ID 映射重写 evidence_refs。
func rewriteEvidenceRefsSourceIDs(refs []EvidenceRef, idMap map[string]string) []EvidenceRef {
	if len(refs) == 0 {
		return refs
	}
	out := make([]EvidenceRef, len(refs))
	for i, ref := range refs {
		if mapped, ok := idMap[ref.SourceID]; ok {
			ref.SourceID = mapped
		}
		out[i] = ref
	}
	return out
}

// mergeSources 合并 sources，primary 优先，fallback 用于补充 synthesizer 额外返回的来源。
func mergeSources(primary, fallback []search.Source) []search.Source {
	if len(primary) == 0 {
		return fallback
	}
	if len(fallback) == 0 {
		return search.DeduplicateStable(primary)
	}
	return search.DeduplicateStable(append(primary, fallback...))
}

// collectLastAssistant 收集 agent 运行过程中最后一条非空 assistant 文本。
//
// ChatModelAgent 可能在工具调用之间产生多条 event；最终 JSON 通常出现在最后一条 assistant
// message 中。
func collectLastAssistant(iterator *adk.AsyncIterator[*adk.AgentEvent]) (string, error) {
	if iterator == nil {
		return "", fmt.Errorf("assistant iterator is nil")
	}

	var last string
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return "", event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		output := event.Output.MessageOutput
		msg, err := output.GetMessage()
		if err != nil {
			return "", err
		}
		if msg == nil {
			continue
		}
		if output.Role != "" && output.Role != schema.Assistant && msg.Role != schema.Assistant {
			continue
		}
		if content := strings.TrimSpace(msg.Content); content != "" {
			last = content
		}
	}
	if strings.TrimSpace(last) == "" {
		return "", fmt.Errorf("assistant output is empty")
	}

	return last, nil
}

// FirstStepPrompt 将 ResearchStep 序列化为 researcher prompt 中的结构化 step 描述。
func (s ResearchStep) FirstStepPrompt() string {
	b, err := json.Marshal(s)
	if err != nil {
		return s.Question
	}
	return string(b)
}
