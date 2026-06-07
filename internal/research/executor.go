package research

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

// Researcher 是单个研究子代理的接口。
//
// 它接收全局问题、当前 step 和历史执行结果，返回该角色视角下的 findings/sources。
type Researcher interface {
	Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error)
}

// ResearcherMetadata 让 executor 能从 researcher 自身读取角色名和 focus。
//
// 这比按数组下标推断角色更稳，尤其适用于 todo dispatcher 动态派发的角色。
type ResearcherMetadata interface {
	ResearcherRole() string
	ResearcherFocus() string
}

// Synthesizer 负责把多个 researcher 的结果综合为一个 TodoExecution。
type Synthesizer interface {
	Synthesize(ctx context.Context, in SynthesisInput) (TodoExecution, error)
}

// ResearcherInput 是传给单个 researcher 的上下文。
type ResearcherInput struct {
	// Question 是全局研究问题或 todo plan objective。
	Question string
	// Step 是当前 researcher 需要执行的具体研究步骤。
	Step ResearchStep
	// ExecutedSteps 是已完成的依赖或上一轮尝试结果，供当前 researcher 避免重复并补缺口。
	ExecutedSteps []priorResearchView
	// Focus 是调度层分配给该 researcher 的研究视角。
	Focus string
}

// SynthesisInput 是传给 synthesizer 的完整并行研究结果。
type SynthesisInput struct {
	// Question 是全局研究问题或 todo plan objective。
	Question string
	// Step 是本轮被综合的研究步骤。
	Step ResearchStep
	// ExecutedSteps 是已完成上下文，帮助 synthesizer 判断是否仍有 gap。
	ExecutedSteps []priorResearchView
	// Results 是所有 researcher 的输出，包括局部失败信息。
	Results []ResearcherResult
}

// StepExecutionInput 是执行一个 ResearchStep 所需的输入。
type StepExecutionInput struct {
	// Question 是全局研究问题或 todo plan objective。
	Question string
	// Step 是要执行的研究步骤。
	Step ResearchStep
	// ExecutedSteps 是 prior context，来自依赖 todo 或 bounded loop 的前几轮尝试。
	ExecutedSteps []priorResearchView
}

// ParallelStepExecutor 并行运行多个 researcher，并在至少一个成功时进入 synthesis。
type ParallelStepExecutor struct {
	// researchers 是并行执行的研究角色列表。
	researchers []Researcher
	// synthesizer 负责把 researchers 的输出合并为 TodoExecution。
	synthesizer Synthesizer
}

// NewParallelStepExecutor 创建一个并行 step executor。
func NewParallelStepExecutor(researchers []Researcher, synthesizer Synthesizer) *ParallelStepExecutor {
	return &ParallelStepExecutor{researchers: researchers, synthesizer: synthesizer}
}

// ExecuteStep 并行调用所有 researcher。
//
// 单个 researcher 失败不会导致整个 step 失败；只有全部 researcher 都失败时才返回错误。
// 这种策略保证反面视角或 freshness 角色失败时，其他证据仍可进入 synthesis。
func (e *ParallelStepExecutor) ExecuteStep(ctx context.Context, in StepExecutionInput) (TodoExecution, error) {
	if e == nil {
		return TodoExecution{}, fmt.Errorf("parallel step executor is nil")
	}
	if isNilDependency(e.synthesizer) {
		return TodoExecution{}, fmt.Errorf("synthesizer is nil")
	}
	for i, researcher := range e.researchers {
		if isNilDependency(researcher) {
			return TodoExecution{}, fmt.Errorf("researcher %d (%s) is nil", i, roleForIndex(i))
		}
	}

	results := make([]ResearcherResult, len(e.researchers))
	researcherErrors := make([]error, len(e.researchers))
	var wg sync.WaitGroup

	for i, researcher := range e.researchers {
		wg.Add(1)
		go func(idx int, r Researcher) {
			defer wg.Done()
			role := researcherRoleForIndex(idx, r)
			focus := researcherFocusForIndex(idx, r)
			result, err := r.Research(ctx, ResearcherInput{
				Question:      in.Question,
				Step:          in.Step,
				ExecutedSteps: in.ExecutedSteps,
				Focus:         focus,
			})
			if err != nil {
				researcherErrors[idx] = err
				results[idx] = ResearcherResult{
					Role:   role,
					Focus:  focus,
					Errors: []string{err.Error()},
				}
				return
			}
			results[idx] = result
		}(i, researcher)
	}

	wg.Wait()
	successes := 0
	for _, result := range results {
		if len(result.Errors) == 0 {
			successes++
		}
	}
	if successes == 0 {
		return TodoExecution{}, allResearchersFailedError(results, researcherErrors)
	}

	return e.synthesizer.Synthesize(ctx, SynthesisInput{
		Question:      in.Question,
		Step:          in.Step,
		ExecutedSteps: in.ExecutedSteps,
		Results:       results,
	})
}

// allResearchersFailedError 汇总全部 researcher 失败原因，便于上层 retry 或诊断。
func allResearchersFailedError(results []ResearcherResult, researcherErrors []error) error {
	errs := []error{errors.New("all researchers failed")}
	for i, result := range results {
		role := result.Role
		if role == "" {
			role = roleForIndex(i)
		}
		if i < len(researcherErrors) && researcherErrors[i] != nil {
			errs = append(errs, fmt.Errorf("%s: %w", role, researcherErrors[i]))
			continue
		}
		for _, msg := range result.Errors {
			errs = append(errs, fmt.Errorf("%s: %s", role, msg))
		}
	}
	return errors.Join(errs...)
}

// researcherRoleForIndex 优先使用 researcher 自带 metadata，缺省时回退到按位置兜底命名。
//
// 生产路径的 AgentResearcher 总会携带非空 role，因此回退分支只在注入的 Researcher
// 未实现 ResearcherMetadata（或返回空角色）时触发。
func researcherRoleForIndex(i int, researcher Researcher) string {
	if metadata, ok := researcher.(ResearcherMetadata); ok {
		if role := strings.TrimSpace(metadata.ResearcherRole()); role != "" {
			return role
		}
	}
	return roleForIndex(i)
}

// researcherFocusForIndex 优先使用 researcher 自带 focus，缺省时回退到按位置兜底 focus。
//
// 与 researcherRoleForIndex 一样，回退分支只为缺 metadata 的注入式 Researcher 兜底。
func researcherFocusForIndex(i int, researcher Researcher) string {
	if metadata, ok := researcher.(ResearcherMetadata); ok {
		if focus := strings.TrimSpace(metadata.ResearcherFocus()); focus != "" {
			return focus
		}
	}
	return focusForIndex(i)
}

// isNilDependency 能识别接口里包着 nil 指针的情况，避免 Go interface nil 陷阱。
func isNilDependency(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// roleForIndex 为缺少 ResearcherMetadata 的 researcher 按位置生成稳定角色名。
//
// 它不是当前派发逻辑的角色来源（角色由 TodoDispatcher 决定），仅作为 Researcher 接口
// 未携带 role 时的防御性兜底。
func roleForIndex(i int) string {
	switch i {
	case 0:
		return "background_researcher"
	case 1:
		return "evidence_researcher"
	default:
		return "counterpoint_researcher"
	}
}

// focusForIndex 为缺少 ResearcherMetadata 的 researcher 按位置生成兜底研究方向。
//
// 与 roleForIndex 配套，仅在注入的 Researcher 未提供 focus 时使用。
func focusForIndex(i int) string {
	switch i {
	case 0:
		return "background, definitions, context, timeline, and key concepts"
	case 1:
		return "data, facts, examples, authoritative evidence, and mainstream positions"
	default:
		return "counterexamples, controversies, limitations, failures, and dissenting views"
	}
}

// sourceIDPrefix 为某个 step/todo 生成局部 source ID 前缀，降低并行 researcher 合并时的
// ID 冲突概率。
func sourceIDPrefix(stepID string) string {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return "src"
	}
	return stepID + "_src"
}

// buildTodoResearchers 根据 TodoDispatcher 产出的 job 构建 researcher。
func buildTodoResearchers(ctx context.Context, m model.ToolCallingChatModel, jobs []TodoResearchJob, researchTools ...tool.BaseTool) ([]Researcher, error) {
	researchers := make([]Researcher, 0, len(jobs))
	for _, job := range jobs {
		researcher, err := NewAgentResearcher(ctx, job.RoleID, job.Focus, m, researchTools...)
		if err != nil {
			return nil, fmt.Errorf("new %s: %w", job.RoleID, err)
		}
		researchers = append(researchers, researcher)
	}
	return researchers, nil
}
