package research

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

type Researcher interface {
	Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error)
}

type Synthesizer interface {
	Synthesize(ctx context.Context, in SynthesisInput) (StepExecution, error)
}

type ResearcherInput struct {
	Question      string
	Step          ResearchStep
	ExecutedSteps []StepExecution
	Focus         string
}

type SynthesisInput struct {
	Question      string
	Step          ResearchStep
	ExecutedSteps []StepExecution
	Results       []ResearcherResult
}

type StepExecutionInput struct {
	Question      string
	Step          ResearchStep
	ExecutedSteps []StepExecution
}

type ParallelStepExecutor struct {
	researchers []Researcher
	synthesizer Synthesizer
}

func NewParallelStepExecutor(researchers []Researcher, synthesizer Synthesizer) *ParallelStepExecutor {
	return &ParallelStepExecutor{researchers: researchers, synthesizer: synthesizer}
}

func (e *ParallelStepExecutor) ExecuteStep(ctx context.Context, in StepExecutionInput) (StepExecution, error) {
	if e == nil {
		return StepExecution{}, fmt.Errorf("parallel step executor is nil")
	}
	if isNilDependency(e.synthesizer) {
		return StepExecution{}, fmt.Errorf("synthesizer is nil")
	}
	for i, researcher := range e.researchers {
		if isNilDependency(researcher) {
			return StepExecution{}, fmt.Errorf("researcher %d (%s) is nil", i, roleForIndex(i))
		}
	}

	results := make([]ResearcherResult, len(e.researchers))
	researcherErrors := make([]error, len(e.researchers))
	var wg sync.WaitGroup

	for i, researcher := range e.researchers {
		wg.Add(1)
		go func(idx int, r Researcher) {
			defer wg.Done()
			focus := focusForIndex(idx)
			result, err := r.Research(ctx, ResearcherInput{
				Question:      in.Question,
				Step:          in.Step,
				ExecutedSteps: in.ExecutedSteps,
				Focus:         focus,
			})
			if err != nil {
				researcherErrors[idx] = err
				results[idx] = ResearcherResult{
					Role:   roleForIndex(idx),
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
		return StepExecution{}, allResearchersFailedError(results, researcherErrors)
	}

	return e.synthesizer.Synthesize(ctx, SynthesisInput{
		Question:      in.Question,
		Step:          in.Step,
		ExecutedSteps: in.ExecutedSteps,
		Results:       results,
	})
}

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
