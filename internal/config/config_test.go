package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLoadDefaultsWhenConfigMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CSE_ID", "")

	cfg, err := Load(LoadOptions{Path: filepath.Join(t.TempDir(), "missing.yaml")})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Search.Provider != "mock" {
		t.Fatalf("Search.Provider = %q, want mock", cfg.Search.Provider)
	}
	if cfg.Research.MaxIterations != 5 {
		t.Fatalf("MaxIterations = %d, want 5", cfg.Research.MaxIterations)
	}
	if cfg.Search.MaxSearchesPerStep != 6 {
		t.Fatalf("MaxSearchesPerStep = %d, want 6", cfg.Search.MaxSearchesPerStep)
	}
	if cfg.Model.Timeout != 60*time.Second {
		t.Fatalf("Timeout = %s, want 60s", cfg.Model.Timeout)
	}
}

func TestLoadConfigFileAndEnvAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "research.yaml")
	data := []byte(`model:
  provider: openai-compatible
  api_key: file-model-key
  model: file-model
  base_url: https://file.example/v1
  timeout: 30s
search:
  provider: google
  google:
    api_key: file-google-key
    cse_id: file-cse
  max_searches_per_step: 4
  results_per_search: 3
research:
  max_iterations: 7
output:
  format: json
  verbose: true
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("OPENAI_API_KEY", "env-model-key")
	t.Setenv("OPENAI_MODEL", "env-model")
	t.Setenv("OPENAI_BASE_URL", "https://env.example/v1")
	t.Setenv("GOOGLE_API_KEY", "env-google-key")
	t.Setenv("GOOGLE_CSE_ID", "env-cse")

	cfg, err := Load(LoadOptions{
		Path: path,
		Overrides: Overrides{
			Provider:      "mock",
			OutputFormat:  "markdown",
			MaxIterations: intPtr(9),
			Verbose:       boolPtr(false),
		},
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Model.APIKey != "env-model-key" {
		t.Fatalf("Model.APIKey = %q, want env-model-key", cfg.Model.APIKey)
	}
	if cfg.Model.Model != "env-model" {
		t.Fatalf("Model.Model = %q, want env-model", cfg.Model.Model)
	}
	if cfg.Search.Google.APIKey != "env-google-key" {
		t.Fatalf("Google.APIKey = %q, want env-google-key", cfg.Search.Google.APIKey)
	}
	if cfg.Search.Provider != "mock" {
		t.Fatalf("Search.Provider = %q, want mock", cfg.Search.Provider)
	}
	if cfg.Research.MaxIterations != 9 {
		t.Fatalf("MaxIterations = %d, want 9", cfg.Research.MaxIterations)
	}
	if cfg.Output.Format != "markdown" {
		t.Fatalf("Output.Format = %q, want markdown", cfg.Output.Format)
	}
	if cfg.Output.Verbose {
		t.Fatalf("Output.Verbose = true, want false")
	}
}

func TestValidateGoogleRequiresCredentials(t *testing.T) {
	cfg := Defaults()
	cfg.Search.Provider = "google"
	cfg.Search.Google.APIKey = ""
	cfg.Search.Google.CSEID = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate returned nil, want missing google credentials error")
	}
}

func TestLoadRejectsNonPositiveMaxIterationsOverride(t *testing.T) {
	for _, value := range []int{0, -1} {
		t.Run("value "+strconv.Itoa(value), func(t *testing.T) {
			_, err := Load(LoadOptions{
				Path: filepath.Join(t.TempDir(), "missing.yaml"),
				Overrides: Overrides{
					MaxIterations: intPtr(value),
				},
			})
			if err == nil {
				t.Fatalf("Load returned nil error for MaxIterations override %d, want error", value)
			}
		})
	}
}

func TestValidateRejectsUnsupportedModelProvider(t *testing.T) {
	cfg := Defaults()
	cfg.Model.Provider = "unsupported"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate returned nil, want unsupported model provider error")
	}
}

func intPtr(v int) *int {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}
