package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/research"
)

type fakeToolCallingModel struct {
	content string
}

func (m fakeToolCallingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.content, nil), nil
}

func (m fakeToolCallingModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(m.content, nil)}), nil
}

func (m fakeToolCallingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestRunExplicitConfigMissingReportsConfigError(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)

	code, stderr := captureStderr(t, func() int {
		return run([]string{"--config", "research.yaml", "question"})
	})

	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr, "config error: read config research.yaml") {
		t.Fatalf("stderr = %q, want config error for missing explicit config", stderr)
	}
	if strings.Contains(stderr, "model error") {
		t.Fatalf("stderr = %q, want config error before model validation", stderr)
	}
}

func TestRunDefaultConfigMissingWithYesContinuesToModelValidation(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)

	code, stderr := captureStderr(t, func() int {
		return run([]string{"--yes", "question"})
	})

	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr, "model error: api key is required") {
		t.Fatalf("stderr = %q, want model validation after default missing config", stderr)
	}
	if strings.Contains(stderr, "config error") {
		t.Fatalf("stderr = %q, want default missing config to be ignored", stderr)
	}
}

func TestRunNonInteractiveRequiresYesOrPlanOnly(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	withNonInteractiveStdin(t)

	code, stderr := captureStderr(t, func() int {
		return run([]string{"question"})
	})

	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr, "non-interactive execution requires --yes or --plan-only") {
		t.Fatalf("stderr = %q, want non-interactive guard", stderr)
	}
	if strings.Contains(stderr, "model error") {
		t.Fatalf("stderr = %q, want guard before model validation", stderr)
	}
}

func TestRunDefaultConfirmationDeclineExitsWithoutExecuting(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	installFakeModel(t)
	withStdin(t, "n\n")
	withInteractiveStdin(t, true)

	code, stdout, stderr := captureOutput(t, func() int {
		return run([]string{"Should we use Eino?"})
	})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty because execution was declined", stdout)
	}
	for _, want := range []string{
		"Objective: Assess whether Eino is suitable.",
		"Continue and execute this plan? [y/N]",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	}
}

func TestRunYesExecutesGeneratedTodoPlan(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)
	installFakeModel(t)
	withNonInteractiveStdin(t)

	code, stdout, stderr := captureOutput(t, func() int {
		return run([]string{"--yes", "Should we use Eino?"})
	})

	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "Continue and execute this plan?") {
		t.Fatalf("stderr = %q, want --yes to skip confirmation", stderr)
	}
	for _, want := range []string{
		"Completed 3 todo(s).",
		"## Execution Summary",
		"- done todo_background: Clarify background",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestRunBlankQuestionReturnsBeforeModelValidation(t *testing.T) {
	clearConfigEnv(t)

	code, stderr := captureStderr(t, func() int {
		return run([]string{"   "})
	})

	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr, "question is required") {
		t.Fatalf("stderr = %q, want question validation error", stderr)
	}
	if strings.Contains(stderr, "model error") {
		t.Fatalf("stderr = %q, want question validation before model validation", stderr)
	}
}

func TestRunHelpReturnsZeroAndPrintsUsage(t *testing.T) {
	code, stderr := captureStderr(t, func() int {
		return run([]string{"--help"})
	})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if !strings.Contains(stderr, "usage: research [flags] \"question\"") {
		t.Fatalf("stderr = %q, want usage", stderr)
	}
	if !strings.Contains(stderr, "-config string") {
		t.Fatalf("stderr = %q, want flag defaults", stderr)
	}
	for _, flag := range []string{"-yes", "-plan-only", "-plan-json", "-max-parallel", "-max-todo-research-iterations"} {
		if !strings.Contains(stderr, flag) {
			t.Fatalf("stderr = %q, want %s flag", stderr, flag)
		}
	}
}

func captureStderr(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	code, _, stderr := captureOutput(t, fn)
	return code, stderr
}

func captureOutput(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()

	oldStderr := os.Stderr
	oldStdout := os.Stdout
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer func() {
		os.Stderr = oldStderr
		os.Stdout = oldStdout
	}()
	os.Stderr = stderrWriter
	os.Stdout = stdoutWriter

	code := fn()

	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}

	return code, string(stdout), string(stderr)
}

func chdir(t *testing.T, dir string) {
	t.Helper()

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func withNonInteractiveStdin(t *testing.T) {
	t.Helper()
	withStdin(t, "")
}

func withStdin(t *testing.T, content string) {
	t.Helper()
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		if err := r.Close(); err != nil {
			t.Errorf("close stdin reader: %v", err)
		}
	})
}

func withInteractiveStdin(t *testing.T, interactive bool) {
	t.Helper()
	old := stdinIsInteractive
	stdinIsInteractive = func() bool {
		return interactive
	}
	t.Cleanup(func() {
		stdinIsInteractive = old
	})
}

func installFakeModel(t *testing.T) {
	t.Helper()
	old := newOpenAICompatibleModel
	newOpenAICompatibleModel = func(context.Context, research.ModelConfig) (model.ToolCallingChatModel, error) {
		return fakeToolCallingModel{content: fakePlanJSON()}, nil
	}
	t.Cleanup(func() {
		newOpenAICompatibleModel = old
	})
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	t.Setenv("OPENAI_MODEL", "test-model")
}

func fakePlanJSON() string {
	return `{
  "objective": "Assess whether Eino is suitable.",
  "sections": [
    {"id": "background", "title": "Background"},
    {"id": "evidence", "title": "Evidence"}
  ],
  "todos": [
    {
      "id": "todo_background",
      "section_id": "background",
      "title": "Clarify background",
      "question": "What is Eino?",
      "search_queries": ["Eino ADK"],
      "acceptance_criteria": ["Defines Eino."]
    },
    {
      "id": "todo_evidence",
      "section_id": "evidence",
      "title": "Collect evidence",
      "question": "What evidence exists?",
      "search_queries": ["Eino planexecute"],
      "acceptance_criteria": ["Collects evidence."],
      "depends_on": ["todo_background"]
    },
    {
      "id": "todo_synthesis",
      "section_id": "evidence",
      "title": "Synthesize recommendation",
      "question": "What should we recommend?",
      "acceptance_criteria": ["Provides a recommendation."],
      "depends_on": ["todo_evidence"]
    }
  ]
}`
}

func clearConfigEnv(t *testing.T) {
	t.Helper()

	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CSE_ID", "")
}
