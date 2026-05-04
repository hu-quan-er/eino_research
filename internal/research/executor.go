package research

import (
	"context"
	"fmt"
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
	results := make([]ResearcherResult, len(e.researchers))
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
		return StepExecution{}, fmt.Errorf("all researchers failed")
	}

	return e.synthesizer.Synthesize(ctx, SynthesisInput{
		Question:      in.Question,
		Step:          in.Step,
		ExecutedSteps: in.ExecutedSteps,
		Results:       results,
	})
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
