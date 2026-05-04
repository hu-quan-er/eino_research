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
)

type AgentResearcher struct {
	role  string
	focus string
	agent adk.Agent
}

func NewAgentResearcher(ctx context.Context, role, focus string, m model.BaseChatModel, searchTool tool.BaseTool) (*AgentResearcher, error) {
	if strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("role is required")
	}
	if strings.TrimSpace(focus) == "" {
		return nil, fmt.Errorf("focus is required")
	}
	if isNilDependency(m) {
		return nil, fmt.Errorf("model is nil")
	}
	if isNilDependency(searchTool) {
		return nil, fmt.Errorf("search tool is nil")
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        role,
		Description: fmt.Sprintf("Research agent focused on %s.", focus),
		Instruction: fmt.Sprintf(`You are %s. Focus on %s.

Use web search when it helps. Return only one JSON object matching:
{
  "role": string,
  "focus": string,
  "queries": [string],
  "findings": [{"claim": string, "rationale": string, "source_ids": [string]}],
  "sources": [{"id": string, "title": string, "url": string, "snippet": string, "provider": string, "query": string}],
  "errors": [string]
}
Do not wrap the JSON in markdown.`, role, focus),
		Model: m,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{searchTool},
			},
		},
		MaxIterations: 4,
	})
	if err != nil {
		return nil, err
	}

	return &AgentResearcher{role: role, focus: focus, agent: agent}, nil
}

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

type AgentSynthesizer struct {
	model model.BaseChatModel
}

func NewAgentSynthesizer(m model.BaseChatModel) *AgentSynthesizer {
	return &AgentSynthesizer{model: m}
}

func (s *AgentSynthesizer) Synthesize(ctx context.Context, in SynthesisInput) (StepExecution, error) {
	if s == nil || isNilDependency(s.model) {
		return StepExecution{}, fmt.Errorf("synthesizer model is nil")
	}

	b, err := json.Marshal(in)
	if err != nil {
		return StepExecution{}, fmt.Errorf("marshal synthesis input: %w", err)
	}

	resp, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(`You synthesize parallel researcher outputs into one StepExecution. Return only valid JSON with fields step, researcher_results, summary, gaps, and sources.`),
		schema.UserMessage(string(b)),
	})
	if err != nil {
		return StepExecution{}, err
	}
	if resp == nil {
		return StepExecution{}, fmt.Errorf("model response is nil")
	}

	content := strings.TrimSpace(resp.Content)
	var out StepExecution
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return StepExecution{
			Step:              in.Step,
			ResearcherResults: in.Results,
			Summary:           content,
		}, nil
	}
	if strings.TrimSpace(out.Step.Question) == "" && strings.TrimSpace(out.Step.Title) == "" {
		out.Step = in.Step
	}
	if out.ResearcherResults == nil {
		out.ResearcherResults = in.Results
	}

	return out, nil
}

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

func (s ResearchStep) FirstStepPrompt() string {
	b, err := json.Marshal(s)
	if err != nil {
		return s.Question
	}
	return string(b)
}
