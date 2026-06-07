package research

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
