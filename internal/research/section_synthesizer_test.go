package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestAgentSectionSynthesizerParsesValidJSON(t *testing.T) {
	model := &staticToolCallingModel{content: `{"section_id":"s1","title":"S1","summary":"section summary","key_findings":["f1 [src_1]"],"limitations":["lim1"]}`}
	synth := NewAgentSectionSynthesizer(model)
	ans, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Question: "Q?",
		Section:  ResearchSection{ID: "s1", Title: "S1"},
		Todos:    []TodoExecution{{Todo: ResearchTodo{ID: "t1"}, Status: TodoDone, Summary: "todo sum"}},
	})
	if err != nil {
		t.Fatalf("SynthesizeSection: %v", err)
	}
	if ans.Summary != "section summary" {
		t.Errorf("summary = %q, want %q", ans.Summary, "section summary")
	}
	if len(ans.KeyFindings) != 1 || ans.KeyFindings[0] != "f1 [src_1]" {
		t.Errorf("key_findings = %#v", ans.KeyFindings)
	}
	if len(ans.Limitations) != 1 {
		t.Errorf("limitations = %#v", ans.Limitations)
	}
}

func TestAgentSectionSynthesizerRejectsNonJSON(t *testing.T) {
	model := &staticToolCallingModel{content: "not json at all"}
	synth := NewAgentSectionSynthesizer(model)
	_, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Section: ResearchSection{ID: "s1"},
	})
	if err == nil {
		t.Fatal("want error on non-JSON section answer")
	}
}

func TestAgentSectionSynthesizerRejectsEmptyAnswer(t *testing.T) {
	model := &staticToolCallingModel{content: `{"section_id":"s1","title":"S1"}`}
	synth := NewAgentSectionSynthesizer(model)
	_, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Section: ResearchSection{ID: "s1"},
	})
	if err == nil {
		t.Fatal("want error on empty section answer (no summary/findings/limitations)")
	}
}

func TestAgentSectionSynthesizerUsesInputIdentityNotModel(t *testing.T) {
	model := &staticToolCallingModel{content: `{"section_id":"WRONG","title":"WRONG","summary":"s","key_findings":["k"]}`}
	synth := NewAgentSectionSynthesizer(model)
	ans, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Section: ResearchSection{ID: "s1", Title: "S1"},
	})
	if err != nil {
		t.Fatalf("SynthesizeSection: %v", err)
	}
	if ans.SectionID != "s1" {
		t.Errorf("SectionID = %q, want s1 (from input, not model)", ans.SectionID)
	}
	if ans.Title != "S1" {
		t.Errorf("Title = %q, want S1 (from input, not model)", ans.Title)
	}
}

func TestSectionExecutionSerializesStructuredFields(t *testing.T) {
	se := SectionExecution{
		Section: ResearchSection{ID: "s1", Title: "S1"},
		Summary: "sum",
	}
	data, err := json.Marshal(se)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"key_findings"`) {
		t.Errorf("key_findings must be omitempty when empty, got %s", data)
	}

	se.KeyFindings = []string{"kf"}
	se.Limitations = []string{"lim"}
	data, _ = json.Marshal(se)
	if !strings.Contains(string(data), `"key_findings"`) || !strings.Contains(string(data), `"limitations"`) {
		t.Errorf("structured fields should serialize when set, got %s", data)
	}
}

type fakeSectionSynthesizer struct {
	failSections map[string]bool
	calls        int
}

func (f *fakeSectionSynthesizer) SynthesizeSection(_ context.Context, in SectionSynthesisInput) (SectionAnswer, error) {
	f.calls++
	if f.failSections[in.Section.ID] {
		return SectionAnswer{}, errors.New("boom")
	}
	return SectionAnswer{
		SectionID:   in.Section.ID,
		Title:       in.Section.Title,
		Summary:     "model summary for " + in.Section.ID,
		KeyFindings: []string{"model finding"},
	}, nil
}

func TestSynthesizeSectionsIsolatesFailures(t *testing.T) {
	fake := &fakeSectionSynthesizer{failSections: map[string]bool{"s2": true}}
	runner, err := NewRunner(RunnerConfig{
		Model:              &staticToolCallingModel{content: "{}"},
		SearchProvider:     search.NewMockProvider(),
		SectionSynthesizer: fake,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	sections := []SectionExecution{
		{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1", Title: "T1"}, Status: TodoDone, Summary: "joined s1", Findings: []Finding{{Claim: "c1", SourceIDs: []string{"src_1"}}}}}},
		{Section: ResearchSection{ID: "s2", Title: "S2"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t2", Title: "T2"}, Status: TodoDone, Summary: "joined s2", Gaps: []string{"missing x"}}}},
	}
	out, answers := runner.synthesizeSections(context.Background(), "Q?", validTodoPlan(), sections)

	if fake.calls != 2 {
		t.Fatalf("synthesizer calls = %d, want 2", fake.calls)
	}
	if len(answers) != 2 {
		t.Fatalf("answers = %d, want 2", len(answers))
	}
	// s1 成功：使用模型 summary。
	if out[0].Summary != "model summary for s1" {
		t.Errorf("s1 summary = %q, want model summary", out[0].Summary)
	}
	if len(out[0].KeyFindings) == 0 {
		t.Errorf("s1 should carry model key findings")
	}
	// s2 失败：走确定性兜底，summary 来自 todo 拼接，不是模型输出。
	if out[1].Summary == "model summary for s2" {
		t.Errorf("s2 summary should be deterministic fallback, got model output")
	}
	if out[1].Summary != "joined s2" {
		t.Errorf("s2 fallback summary = %q, want %q", out[1].Summary, "joined s2")
	}
}

func TestSynthesizeSectionsDefaultsToAgentWhenNil(t *testing.T) {
	// SectionSynthesizer 为 nil 时使用模型默认实现；模型返回合法 SectionAnswer。
	runner, err := NewRunner(RunnerConfig{
		Model:          &staticToolCallingModel{content: `{"summary":"agent default summary","key_findings":["kf"]}`},
		SearchProvider: search.NewMockProvider(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	sections := []SectionExecution{
		{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1"}, Status: TodoDone, Summary: "joined"}}},
	}
	out, answers := runner.synthesizeSections(context.Background(), "Q?", validTodoPlan(), sections)
	if len(answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(answers))
	}
	if out[0].Summary != "agent default summary" {
		t.Errorf("summary = %q, want agent default", out[0].Summary)
	}
}

func TestSynthesizeSectionsEmitsSectionEvents(t *testing.T) {
	rec := &recordingSink{}
	bus := &EventBus{}
	bus.Add(rec)
	fake := &fakeSectionSynthesizer{}
	runner, err := NewRunner(RunnerConfig{
		Model:              &staticToolCallingModel{content: "{}"},
		SearchProvider:     search.NewMockProvider(),
		SectionSynthesizer: fake,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	ctx := withEventBus(context.Background(), bus)
	sections := []SectionExecution{
		{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1"}, Status: TodoDone, Summary: "x"}}},
	}
	runner.synthesizeSections(ctx, "Q?", validTodoPlan(), sections)

	kinds := make(map[EventKind]int)
	for _, e := range rec.Snapshot() {
		kinds[e.Kind]++
	}
	if kinds[EventSectionStarted] == 0 || kinds[EventSectionCompleted] == 0 {
		t.Fatalf("missing section events; got %v", kinds)
	}
}

func TestBudgetMeterCountsSectionCompleted(t *testing.T) {
	meter := NewBudgetMeter()
	meter.Emit(context.Background(), Event{Kind: EventSectionCompleted})
	if meter.Snapshot().ModelCalls != 1 {
		t.Fatalf("ModelCalls = %d, want 1", meter.Snapshot().ModelCalls)
	}
}
