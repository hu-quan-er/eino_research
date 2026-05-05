package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

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

func TestRunDefaultConfigMissingContinuesToModelValidation(t *testing.T) {
	chdir(t, t.TempDir())
	clearConfigEnv(t)

	code, stderr := captureStderr(t, func() int {
		return run([]string{"question"})
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
}

func captureStderr(t *testing.T, fn func() int) (int, string) {
	t.Helper()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	defer func() {
		os.Stderr = oldStderr
	}()
	os.Stderr = w

	code := fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}

	return code, string(out)
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

func clearConfigEnv(t *testing.T) {
	t.Helper()

	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CSE_ID", "")
}
