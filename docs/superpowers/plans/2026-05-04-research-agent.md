# Research Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI Deep Research Agent using Eino `prebuilt/planexecute`, a custom `ResearchPlan`, and a custom parallel researcher executor.

**Architecture:** Keep CLI, config, search providers, rendering, and Eino-specific research orchestration in separate packages. Use pure Go interfaces for search and step execution so most tests run without real OpenAI or Google credentials. Use Eino only inside `internal/research` adapters and runner.

**Tech Stack:** Go 1.26, Eino v0.8.13, eino-ext OpenAI chat model v0.1.13, Google Custom Search JSON API, `gopkg.in/yaml.v3`, standard `flag`, `net/http`, and `testing`.

---

## File Structure

- Create: `go.mod`
- Create: `cmd/research/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/search/provider.go`
- Create: `internal/search/mock.go`
- Create: `internal/search/google.go`
- Create: `internal/search/search_test.go`
- Create: `internal/research/plan.go`
- Create: `internal/research/result.go`
- Create: `internal/research/tools.go`
- Create: `internal/research/executor.go`
- Create: `internal/research/runner.go`
- Create: `internal/research/model.go`
- Create: `internal/research/plan_test.go`
- Create: `internal/research/tools_test.go`
- Create: `internal/research/executor_test.go`
- Create: `internal/render/markdown.go`
- Create: `internal/render/json.go`
- Create: `internal/render/render_test.go`
- Create: `research.example.yaml`
- Create: `README.md`

## Task 1: Module Skeleton and Dependencies

**Files:**
- Create: `go.mod`
- Create: `cmd/research/main.go`

- [ ] **Step 1: Initialize the module**

Run:

```bash
go mod init github.com/hu-quan-er/eino_research
```

Expected: `go.mod` exists with module path `github.com/hu-quan-er/eino_research`.

- [ ] **Step 2: Add required dependencies**

Run:

```bash
go get github.com/cloudwego/eino@v0.8.13 github.com/cloudwego/eino-ext/components/model/openai@v0.1.13 gopkg.in/yaml.v3
```

Expected: dependencies are added to `go.mod`.

- [ ] **Step 3: Create a temporary CLI entrypoint**

Create `cmd/research/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("research agent bootstrap")
}
```

- [ ] **Step 4: Verify bootstrap builds**

Run:

```bash
go run ./cmd/research
```

Expected output:

```text
research agent bootstrap
```

- [ ] **Step 5: Commit**

Run:

```bash
git add go.mod go.sum cmd/research/main.go
git commit -m "chore: initialize go module"
```

Expected: commit succeeds.

## Task 2: Configuration Loading

**Files:**
- Create: `internal/config/config_test.go`
- Create: `internal/config/config.go`
- Create: `research.example.yaml`

- [ ] **Step 1: Write failing config tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
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
			MaxIterations: 9,
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

func boolPtr(v bool) *bool {
	return &v
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/config
```

Expected: FAIL because package `internal/config` and functions `Load`, `Defaults`, and `Validate` do not exist.

- [ ] **Step 3: Implement config loading**

Create `internal/config/config.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", raw, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

type Config struct {
	Model    ModelConfig    `yaml:"model"`
	Search   SearchConfig   `yaml:"search"`
	Research ResearchConfig `yaml:"research"`
	Output   OutputConfig   `yaml:"output"`
}

type ModelConfig struct {
	Provider string        `yaml:"provider"`
	APIKey   string        `yaml:"api_key"`
	Model    string        `yaml:"model"`
	BaseURL  string        `yaml:"base_url"`
	Timeout  time.Duration `yaml:"-"`
	RawTime  Duration      `yaml:"timeout"`
}

type SearchConfig struct {
	Provider             string             `yaml:"provider"`
	Google               GoogleSearchConfig `yaml:"google"`
	MaxSearchesPerStep   int                `yaml:"max_searches_per_step"`
	ResultsPerSearch     int                `yaml:"results_per_search"`
}

type GoogleSearchConfig struct {
	APIKey string `yaml:"api_key"`
	CSEID  string `yaml:"cse_id"`
}

type ResearchConfig struct {
	MaxIterations  int      `yaml:"max_iterations"`
	ResearcherRoles []string `yaml:"researcher_roles"`
}

type OutputConfig struct {
	Format  string `yaml:"format"`
	Verbose bool   `yaml:"verbose"`
}

type LoadOptions struct {
	Path      string
	Explicit  bool
	Overrides Overrides
}

type Overrides struct {
	Provider      string
	OutputFormat  string
	MaxIterations int
	Verbose       *bool
}

func Defaults() Config {
	return Config{
		Model: ModelConfig{
			Provider: "openai-compatible",
			Timeout:  60 * time.Second,
			RawTime:  Duration{Duration: 60 * time.Second},
		},
		Search: SearchConfig{
			Provider:           "mock",
			MaxSearchesPerStep: 6,
			ResultsPerSearch:   5,
		},
		Research: ResearchConfig{
			MaxIterations: 5,
			ResearcherRoles: []string{
				"background_researcher",
				"evidence_researcher",
				"counterpoint_researcher",
			},
		},
		Output: OutputConfig{
			Format: "markdown",
		},
	}
}

func Load(opts LoadOptions) (Config, error) {
	cfg := Defaults()
	if opts.Path != "" {
		b, err := os.ReadFile(opts.Path)
		if err != nil {
			if opts.Explicit || !errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("read config %s: %w", opts.Path, err)
			}
		} else if err := yaml.Unmarshal(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", opts.Path, err)
		}
	}
	if cfg.Model.RawTime.Duration != 0 {
		cfg.Model.Timeout = cfg.Model.RawTime.Duration
	}
	applyEnv(&cfg)
	applyOverrides(&cfg, opts.Overrides)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.Model.APIKey = v
	}
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		cfg.Model.Model = v
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		cfg.Model.BaseURL = v
	}
	if v := os.Getenv("GOOGLE_API_KEY"); v != "" {
		cfg.Search.Google.APIKey = v
	}
	if v := os.Getenv("GOOGLE_CSE_ID"); v != "" {
		cfg.Search.Google.CSEID = v
	}
}

func applyOverrides(cfg *Config, o Overrides) {
	if o.Provider != "" {
		cfg.Search.Provider = o.Provider
	}
	if o.OutputFormat != "" {
		cfg.Output.Format = o.OutputFormat
	}
	if o.MaxIterations > 0 {
		cfg.Research.MaxIterations = o.MaxIterations
	}
	if o.Verbose != nil {
		cfg.Output.Verbose = *o.Verbose
	}
}

func (c Config) Validate() error {
	if c.Search.Provider != "mock" && c.Search.Provider != "google" {
		return fmt.Errorf("unsupported search provider %q", c.Search.Provider)
	}
	if c.Search.Provider == "google" {
		if c.Search.Google.APIKey == "" || c.Search.Google.CSEID == "" {
			return errors.New("google search requires GOOGLE_API_KEY and GOOGLE_CSE_ID or config search.google credentials")
		}
	}
	if c.Output.Format != "markdown" && c.Output.Format != "json" {
		return fmt.Errorf("unsupported output format %q", c.Output.Format)
	}
	if c.Research.MaxIterations <= 0 {
		return errors.New("research.max_iterations must be positive")
	}
	if c.Search.MaxSearchesPerStep <= 0 {
		return errors.New("search.max_searches_per_step must be positive")
	}
	if c.Search.ResultsPerSearch <= 0 {
		return errors.New("search.results_per_search must be positive")
	}
	return nil
}
```

- [ ] **Step 4: Create example config**

Create `research.example.yaml`:

```yaml
model:
  provider: openai-compatible
  api_key: ""
  model: gpt-4.1
  base_url: ""
  timeout: 60s

search:
  provider: mock
  google:
    api_key: ""
    cse_id: ""
  max_searches_per_step: 6
  results_per_search: 5

research:
  max_iterations: 5
  researcher_roles:
    - background_researcher
    - evidence_researcher
    - counterpoint_researcher

output:
  format: markdown
  verbose: false
```

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```bash
go test ./internal/config
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/config research.example.yaml go.mod go.sum
git commit -m "feat: add configuration loading"
```

Expected: commit succeeds.

## Task 3: Search Providers and Source Deduplication

**Files:**
- Create: `internal/search/provider.go`
- Create: `internal/search/mock.go`
- Create: `internal/search/google.go`
- Create: `internal/search/search_test.go`

- [ ] **Step 1: Write failing search tests**

Create `internal/search/search_test.go`:

```go
package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockProviderReturnsSources(t *testing.T) {
	p := NewMockProvider()
	got, err := p.Search(context.Background(), "eino agent", 2)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Provider != "mock" {
		t.Fatalf("Provider = %q, want mock", got[0].Provider)
	}
	if got[0].Query != "eino agent" {
		t.Fatalf("Query = %q, want eino agent", got[0].Query)
	}
}

func TestDeduplicateSourcesByURL(t *testing.T) {
	in := []Source{
		{ID: "a", URL: "https://example.com/a", Title: "A"},
		{ID: "b", URL: "https://example.com/a", Title: "B"},
		{ID: "c", URL: "https://example.com/c", Title: "C"},
	}
	got := Deduplicate(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "src_1" || got[1].ID != "src_2" {
		t.Fatalf("IDs = %q, %q; want src_1, src_2", got[0].ID, got[1].ID)
	}
}

func TestGoogleProviderParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "eino agent" {
			t.Fatalf("q = %q, want eino agent", got)
		}
		if got := r.URL.Query().Get("num"); got != "2" {
			t.Fatalf("num = %q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items": [
				{"title": "One", "link": "https://example.com/1", "snippet": "First"},
				{"title": "Two", "link": "https://example.com/2", "snippet": "Second"}
			]
		}`))
	}))
	defer server.Close()

	p := NewGoogleProvider(GoogleConfig{
		APIKey:  "key",
		CSEID:   "cx",
		BaseURL: server.URL,
		Client:  server.Client(),
	})

	got, err := p.Search(context.Background(), "eino agent", 2)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Title != "One" || got[0].Provider != "google" {
		t.Fatalf("first source = %+v", got[0])
	}
}

func TestGoogleProviderHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	p := NewGoogleProvider(GoogleConfig{
		APIKey:  "key",
		CSEID:   "cx",
		BaseURL: server.URL,
		Client:  server.Client(),
	})

	_, err := p.Search(context.Background(), "bad query", 1)
	if err == nil {
		t.Fatal("Search returned nil error, want HTTP error")
	}
	if !strings.Contains(err.Error(), "google") || !strings.Contains(err.Error(), "bad query") {
		t.Fatalf("error = %q, want provider and query", err.Error())
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/search
```

Expected: FAIL because package symbols do not exist.

- [ ] **Step 3: Implement provider types**

Create `internal/search/provider.go`:

```go
package search

import "context"

type Source struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
	Provider string `json:"provider"`
	Query    string `json:"query"`
}

type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]Source, error)
}

func Deduplicate(in []Source) []Source {
	seen := make(map[string]bool)
	out := make([]Source, 0, len(in))
	for _, src := range in {
		if src.URL == "" || seen[src.URL] {
			continue
		}
		seen[src.URL] = true
		src.ID = "src_" + itoa(len(out)+1)
		out = append(out, src)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
```

- [ ] **Step 4: Implement mock provider**

Create `internal/search/mock.go`:

```go
package search

import (
	"context"
	"fmt"
)

type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p *MockProvider) Search(ctx context.Context, query string, limit int) ([]Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("mock search limit must be positive")
	}
	out := make([]Source, 0, limit)
	for i := 1; i <= limit; i++ {
		out = append(out, Source{
			ID:       fmt.Sprintf("src_%d", i),
			Title:    fmt.Sprintf("Mock result %d for %s", i, query),
			URL:      fmt.Sprintf("https://example.com/mock/%d", i),
			Snippet:  fmt.Sprintf("Mock snippet %d for query %q", i, query),
			Provider: "mock",
			Query:    query,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Implement Google provider**

Create `internal/search/google.go`:

```go
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type GoogleConfig struct {
	APIKey  string
	CSEID   string
	BaseURL string
	Client  *http.Client
}

type GoogleProvider struct {
	cfg GoogleConfig
}

func NewGoogleProvider(cfg GoogleConfig) *GoogleProvider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://www.googleapis.com/customsearch/v1"
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &GoogleProvider{cfg: cfg}
}

func (p *GoogleProvider) Search(ctx context.Context, query string, limit int) ([]Source, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("google search limit must be positive")
	}
	u, err := url.Parse(p.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("google search base url: %w", err)
	}
	q := u.Query()
	q.Set("key", p.cfg.APIKey)
	q.Set("cx", p.cfg.CSEID)
	q.Set("q", query)
	q.Set("num", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("google search request for query %q: %w", query, err)
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google search query %q: %w", query, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("google search query %q failed with status %d", query, resp.StatusCode)
	}

	var body struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("google search decode query %q: %w", query, err)
	}

	out := make([]Source, 0, len(body.Items))
	for i, item := range body.Items {
		out = append(out, Source{
			ID:       fmt.Sprintf("src_%d", i+1),
			Title:    item.Title,
			URL:      item.Link,
			Snippet:  item.Snippet,
			Provider: "google",
			Query:    query,
		})
	}
	return out, nil
}
```

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```bash
go test ./internal/search
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/search
git commit -m "feat: add search providers"
```

Expected: commit succeeds.

## Task 4: Research Data Structures and Plan Contract

**Files:**
- Create: `internal/research/plan_test.go`
- Create: `internal/research/plan.go`
- Create: `internal/research/result.go`

- [ ] **Step 1: Write failing plan tests**

Create `internal/research/plan_test.go`:

```go
package research

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
)

func TestResearchPlanImplementsPlan(t *testing.T) {
	var _ planexecute.Plan = (*ResearchPlan)(nil)
}

func TestResearchPlanFirstStepReturnsJSON(t *testing.T) {
	plan := &ResearchPlan{Steps: []ResearchStep{{
		ID:              "step_1",
		Title:           "Map context",
		Question:        "What is Eino?",
		SearchQueries:   []string{"Eino framework"},
		ResearchAxes:    []string{"background"},
		SuccessCriteria: []string{"explain context"},
	}}}

	first := plan.FirstStep()
	var step ResearchStep
	if err := json.Unmarshal([]byte(first), &step); err != nil {
		t.Fatalf("FirstStep returned invalid JSON: %v", err)
	}
	if step.ID != "step_1" {
		t.Fatalf("ID = %q, want step_1", step.ID)
	}
}

func TestResearchPlanEmptyFirstStep(t *testing.T) {
	plan := &ResearchPlan{}
	if got := plan.FirstStep(); got != "" {
		t.Fatalf("FirstStep = %q, want empty string", got)
	}
}

func TestResearchPlanJSONRoundTrip(t *testing.T) {
	original := &ResearchPlan{Steps: []ResearchStep{{
		ID:              "step_1",
		Title:           "Evidence",
		Question:        "What evidence exists?",
		SearchQueries:   []string{"eino evidence"},
		SuccessCriteria: []string{"find evidence"},
	}}}

	b, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var decoded ResearchPlan
	if err := decoded.UnmarshalJSON(b); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if decoded.Steps[0].Title != "Evidence" {
		t.Fatalf("Title = %q, want Evidence", decoded.Steps[0].Title)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/research -run TestResearchPlan
```

Expected: FAIL because `ResearchPlan` and `ResearchStep` do not exist.

- [ ] **Step 3: Implement plan and result types**

Create `internal/research/plan.go`:

```go
package research

import "encoding/json"

type ResearchPlan struct {
	Steps []ResearchStep `json:"steps"`
}

type ResearchStep struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Question        string   `json:"question"`
	SearchQueries   []string `json:"search_queries"`
	ResearchAxes    []string `json:"research_axes,omitempty"`
	SuccessCriteria []string `json:"success_criteria"`
}

func (p *ResearchPlan) FirstStep() string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	b, err := json.Marshal(p.Steps[0])
	if err != nil {
		return ""
	}
	return string(b)
}

func (p *ResearchPlan) MarshalJSON() ([]byte, error) {
	type alias ResearchPlan
	return json.Marshal((*alias)(p))
}

func (p *ResearchPlan) UnmarshalJSON(b []byte) error {
	type alias ResearchPlan
	return json.Unmarshal(b, (*alias)(p))
}
```

Create `internal/research/result.go`:

```go
package research

import "github.com/hu-quan-er/eino_research/internal/search"

type ResearchResult struct {
	Question      string          `json:"question"`
	Answer        Answer          `json:"answer"`
	Plan          ResearchPlan    `json:"plan"`
	ExecutedSteps []StepExecution `json:"executed_steps"`
	Sources       []search.Source `json:"sources"`
	Metadata      Metadata        `json:"metadata"`
	Error         *RunError       `json:"error,omitempty"`
}

type Answer struct {
	Markdown    string   `json:"markdown"`
	Summary     string   `json:"summary"`
	KeyFindings []string `json:"key_findings"`
	Limitations []string `json:"limitations"`
}

type StepExecution struct {
	Step              ResearchStep       `json:"step"`
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	Summary           string             `json:"summary"`
	Gaps              []string           `json:"gaps,omitempty"`
	Sources           []search.Source    `json:"sources"`
}

type ResearcherResult struct {
	Role     string          `json:"role"`
	Focus    string          `json:"focus"`
	Queries  []string        `json:"queries"`
	Findings []Finding       `json:"findings"`
	Sources  []search.Source `json:"sources"`
	Errors   []string        `json:"errors,omitempty"`
}

type Finding struct {
	Claim     string   `json:"claim"`
	Rationale string   `json:"rationale"`
	SourceIDs []string `json:"source_ids"`
}

type Metadata struct {
	Model          string `json:"model"`
	SearchProvider string `json:"search_provider"`
	MaxIterations  int    `json:"max_iterations"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at"`
	DurationMS     int64  `json:"duration_ms"`
}

type RunError struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/research -run TestResearchPlan
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/research/plan.go internal/research/result.go internal/research/plan_test.go go.mod go.sum
git commit -m "feat: add research plan contract"
```

Expected: commit succeeds.

## Task 5: Renderers

**Files:**
- Create: `internal/render/render_test.go`
- Create: `internal/render/markdown.go`
- Create: `internal/render/json.go`

- [ ] **Step 1: Write failing renderer tests**

Create `internal/render/render_test.go`:

```go
package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
)

func sampleResult() research.ResearchResult {
	return research.ResearchResult{
		Question: "Should we use Eino?",
		Answer: research.Answer{
			Markdown:    "# 结论\n\nUse Eino for this prototype.",
			Summary:     "Use Eino for this prototype.",
			KeyFindings: []string{"Eino has ADK."},
			Limitations: []string{"No HTTP service in v1."},
		},
		Sources: []search.Source{{
			ID: "src_1", Title: "Eino", URL: "https://example.com/eino", Provider: "mock",
		}},
	}
}

func TestMarkdownIncludesAnswerAndSources(t *testing.T) {
	out := Markdown(sampleResult())
	for _, want := range []string{"Use Eino", "## Sources", "https://example.com/eino"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, out)
		}
	}
}

func TestJSONIsResearchResult(t *testing.T) {
	out, err := JSON(sampleResult())
	if err != nil {
		t.Fatalf("JSON returned error: %v", err)
	}
	var decoded research.ResearchResult
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if decoded.Question != "Should we use Eino?" {
		t.Fatalf("Question = %q", decoded.Question)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/render
```

Expected: FAIL because package functions do not exist.

- [ ] **Step 3: Implement Markdown renderer**

Create `internal/render/markdown.go`:

```go
package render

import (
	"fmt"
	"strings"

	"github.com/hu-quan-er/eino_research/internal/research"
)

func Markdown(result research.ResearchResult) string {
	if result.Answer.Markdown != "" {
		var sb strings.Builder
		sb.WriteString(result.Answer.Markdown)
		sb.WriteString("\n\n")
		appendSources(&sb, result)
		return strings.TrimSpace(sb.String()) + "\n"
	}

	var sb strings.Builder
	sb.WriteString("# Research Report\n\n")
	sb.WriteString("## Question\n\n")
	sb.WriteString(result.Question)
	sb.WriteString("\n\n## Summary\n\n")
	sb.WriteString(result.Answer.Summary)
	sb.WriteString("\n\n")
	appendSources(&sb, result)
	return strings.TrimSpace(sb.String()) + "\n"
}

func appendSources(sb *strings.Builder, result research.ResearchResult) {
	if len(result.Sources) == 0 {
		return
	}
	sb.WriteString("## Sources\n\n")
	for _, src := range result.Sources {
		sb.WriteString(fmt.Sprintf("- [%s] %s", src.ID, src.Title))
		if src.URL != "" {
			sb.WriteString(fmt.Sprintf(" - %s", src.URL))
		}
		sb.WriteString("\n")
	}
}
```

- [ ] **Step 4: Implement JSON renderer**

Create `internal/render/json.go`:

```go
package render

import (
	"encoding/json"

	"github.com/hu-quan-er/eino_research/internal/research"
)

func JSON(result research.ResearchResult) (string, error) {
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
```

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```bash
go test ./internal/render
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/render
git commit -m "feat: add research renderers"
```

Expected: commit succeeds.

## Task 6: Web Search Tool and Limits

**Files:**
- Create: `internal/research/tools_test.go`
- Create: `internal/research/tools.go`

- [ ] **Step 1: Write failing tool tests**

Create `internal/research/tools_test.go`:

```go
package research

import (
	"context"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

type recordingProvider struct {
	calls []string
}

func (p *recordingProvider) Search(ctx context.Context, query string, limit int) ([]search.Source, error) {
	p.calls = append(p.calls, query)
	return []search.Source{{ID: "src_1", Title: "Result", URL: "https://example.com", Provider: "mock", Query: query}}, nil
}

func TestWebSearchToolRunsProvider(t *testing.T) {
	provider := &recordingProvider{}
	tool, err := NewWebSearchTool(provider, SearchLimits{MaxSearchesPerStep: 2, ResultsPerSearch: 5})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}

	out, err := tool.InvokableRun(context.Background(), `{"query":"eino","limit":3}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "https://example.com") {
		t.Fatalf("output = %s", out)
	}
	if len(provider.calls) != 1 || provider.calls[0] != "eino" {
		t.Fatalf("calls = %+v", provider.calls)
	}
}

func TestWebSearchToolEnforcesLimit(t *testing.T) {
	provider := &recordingProvider{}
	tool, err := NewWebSearchTool(provider, SearchLimits{MaxSearchesPerStep: 1, ResultsPerSearch: 5})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}

	if _, err := tool.InvokableRun(context.Background(), `{"query":"one"}`); err != nil {
		t.Fatalf("first search returned error: %v", err)
	}
	_, err = tool.InvokableRun(context.Background(), `{"query":"two"}`)
	if err == nil {
		t.Fatal("second search returned nil error, want limit error")
	}
	if !strings.Contains(err.Error(), "search limit exceeded") {
		t.Fatalf("error = %q", err.Error())
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/research -run TestWebSearchTool
```

Expected: FAIL because `NewWebSearchTool` does not exist.

- [ ] **Step 3: Implement web search tool**

Create `internal/research/tools.go`:

```go
package research

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/hu-quan-er/eino_research/internal/search"
)

type SearchLimits struct {
	MaxSearchesPerStep int
	ResultsPerSearch   int
}

type WebSearchInput struct {
	Query string `json:"query" jsonschema:"description=Search query to run"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return"`
}

func NewWebSearchTool(provider search.Provider, limits SearchLimits) (tool.InvokableTool, error) {
	var count atomic.Int64
	return utils.InferTool("web_search", "Search the web for current research sources.", func(ctx context.Context, input WebSearchInput) ([]search.Source, error) {
		if input.Query == "" {
			return nil, fmt.Errorf("query is required")
		}
		next := count.Add(1)
		if limits.MaxSearchesPerStep > 0 && int(next) > limits.MaxSearchesPerStep {
			return nil, fmt.Errorf("search limit exceeded for current step")
		}
		limit := input.Limit
		if limit <= 0 {
			limit = limits.ResultsPerSearch
		}
		if limit <= 0 {
			limit = 5
		}
		return provider.Search(ctx, input.Query, limit)
	})
}
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/research -run TestWebSearchTool
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/research/tools.go internal/research/tools_test.go go.mod go.sum
git commit -m "feat: add web search tool"
```

Expected: commit succeeds.

## Task 7: Parallel Step Executor Core

**Files:**
- Create: `internal/research/executor_test.go`
- Create: `internal/research/executor.go`

- [ ] **Step 1: Write failing executor tests**

Create `internal/research/executor_test.go`:

```go
package research

import (
	"context"
	"errors"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

type fakeResearcher struct {
	role string
	err  error
}

func (r fakeResearcher) Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error) {
	if r.err != nil {
		return ResearcherResult{}, r.err
	}
	return ResearcherResult{
		Role:  r.role,
		Focus: in.Focus,
		Queries: []string{in.Step.Question},
		Findings: []Finding{{Claim: r.role + " finding", Rationale: "test rationale"}},
		Sources: []search.Source{{ID: r.role, Title: r.role, URL: "https://example.com/" + r.role}},
	}, nil
}

type fakeSynthesizer struct{}

func (s fakeSynthesizer) Synthesize(ctx context.Context, in SynthesisInput) (StepExecution, error) {
	return StepExecution{
		Step:              in.Step,
		ResearcherResults: in.Results,
		Summary:           "combined",
		Sources:           []search.Source{{ID: "src_1", Title: "combined", URL: "https://example.com/combined"}},
	}, nil
}

func TestParallelStepExecutorRunsAllResearchers(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher"},
		fakeResearcher{role: "evidence_researcher"},
		fakeResearcher{role: "counterpoint_researcher"},
	}, fakeSynthesizer{})

	out, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step: ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	if err != nil {
		t.Fatalf("ExecuteStep returned error: %v", err)
	}
	if out.Summary != "combined" {
		t.Fatalf("Summary = %q, want combined", out.Summary)
	}
	if len(out.ResearcherResults) != 3 {
		t.Fatalf("researcher results = %d, want 3", len(out.ResearcherResults))
	}
}

func TestParallelStepExecutorContinuesWhenOneResearcherFails(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher"},
		fakeResearcher{role: "evidence_researcher", err: errors.New("model failed")},
		fakeResearcher{role: "counterpoint_researcher"},
	}, fakeSynthesizer{})

	out, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step: ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	if err != nil {
		t.Fatalf("ExecuteStep returned error: %v", err)
	}
	if len(out.ResearcherResults) != 3 {
		t.Fatalf("researcher results = %d, want 3 including failed result", len(out.ResearcherResults))
	}
	if len(out.ResearcherResults[1].Errors) == 0 {
		t.Fatalf("failed researcher errors = 0, want error recorded")
	}
}

func TestParallelStepExecutorFailsWhenAllResearchersFail(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher", err: errors.New("failed")},
		fakeResearcher{role: "evidence_researcher", err: errors.New("failed")},
		fakeResearcher{role: "counterpoint_researcher", err: errors.New("failed")},
	}, fakeSynthesizer{})

	_, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step: ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	if err == nil {
		t.Fatal("ExecuteStep returned nil error, want all researchers failed")
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/research -run TestParallelStepExecutor
```

Expected: FAIL because executor types do not exist.

- [ ] **Step 3: Implement executor core**

Create `internal/research/executor.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/research -run TestParallelStepExecutor
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/research/executor.go internal/research/executor_test.go
git commit -m "feat: add parallel step executor"
```

Expected: commit succeeds.

## Task 8: Eino Model Factory, Agents, and Runner

**Files:**
- Create: `internal/research/model.go`
- Create: `internal/research/runner.go`
- Modify: `internal/research/executor.go`

- [ ] **Step 1: Write compile-oriented runner tests**

Append to `internal/research/executor_test.go`:

```go
func TestBuildDefaultResearcherRoles(t *testing.T) {
	roles := DefaultResearcherRoles()
	want := []string{"background_researcher", "evidence_researcher", "counterpoint_researcher"}
	if len(roles) != len(want) {
		t.Fatalf("roles length = %d, want %d", len(roles), len(want))
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles[%d] = %q, want %q", i, roles[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
go test ./internal/research -run TestBuildDefaultResearcherRoles
```

Expected: FAIL because `DefaultResearcherRoles` does not exist.

- [ ] **Step 3: Add role helper**

Append to `internal/research/executor.go`:

```go
func DefaultResearcherRoles() []string {
	return []string{
		"background_researcher",
		"evidence_researcher",
		"counterpoint_researcher",
	}
}
```

- [ ] **Step 4: Add OpenAI-compatible model factory**

Create `internal/research/model.go`:

```go
package research

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

type ModelConfig struct {
	APIKey  string
	Model   string
	BaseURL string
	Timeout time.Duration
}

func NewOpenAICompatibleModel(ctx context.Context, cfg ModelConfig) (model.ToolCallingChatModel, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("model api key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
		Timeout: cfg.Timeout,
	})
}
```

- [ ] **Step 5: Add model-backed researcher and synthesizer adapters**

Create `internal/research/researchers.go`:

```go
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
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        role,
		Description: "researcher focused on " + focus,
		Instruction: "You are " + role + ". Focus on " + focus + ". Return JSON with role, focus, queries, findings, sources, and errors.",
		Model:       m,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{searchTool}},
		},
		MaxIterations: 4,
	})
	if err != nil {
		return nil, err
	}
	return &AgentResearcher{role: role, focus: focus, agent: agent}, nil
}

func (r *AgentResearcher) Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error) {
	prompt := fmt.Sprintf("Question: %s\nStep: %s\nFocus: %s\nReturn only JSON.", in.Question, in.Step.FirstStepPrompt(), r.focus)
	iter := adk.NewRunner(ctx, adk.RunnerConfig{Agent: r.agent}).Query(ctx, prompt)
	content, err := collectLastAssistant(iter)
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
	if result.Role == "" {
		result.Role = r.role
	}
	if result.Focus == "" {
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
	b, err := json.Marshal(in)
	if err != nil {
		return StepExecution{}, err
	}
	resp, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage("Synthesize researcher results into one StepExecution JSON. Return only JSON."),
		schema.UserMessage(string(b)),
	})
	if err != nil {
		return StepExecution{}, err
	}
	var execution StepExecution
	if err := json.Unmarshal([]byte(resp.Content), &execution); err != nil {
		execution = StepExecution{
			Step:              in.Step,
			ResearcherResults: in.Results,
			Summary:           strings.TrimSpace(resp.Content),
		}
	}
	if execution.Step.ID == "" {
		execution.Step = in.Step
	}
	return execution, nil
}

func collectLastAssistant(iter *adk.AsyncIterator[*adk.AgentEvent]) (string, error) {
	var last string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return "", event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		msg, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			return "", err
		}
		if msg != nil && msg.Role == schema.Assistant {
			last = msg.Content
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
```

- [ ] **Step 6: Add runner assembly**

Create `internal/research/runner.go`:

```go
package research

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

type RunnerConfig struct {
	Model              model.ToolCallingChatModel
	SearchProvider     search.Provider
	ModelName          string
	SearchProviderName string
	MaxIterations      int
	MaxSearchesPerStep int
	ResultsPerSearch   int
}

type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("model is required")
	}
	if cfg.SearchProvider == nil {
		return nil, fmt.Errorf("search provider is required")
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 5
	}
	if cfg.MaxSearchesPerStep <= 0 {
		cfg.MaxSearchesPerStep = 6
	}
	if cfg.ResultsPerSearch <= 0 {
		cfg.ResultsPerSearch = 5
	}
	return &Runner{cfg: cfg}, nil
}

func (r *Runner) Run(ctx context.Context, question string) (ResearchResult, error) {
	started := time.Now()
	result := ResearchResult{
		Question: question,
		Metadata: Metadata{
			Model:          r.cfg.ModelName,
			SearchProvider: r.cfg.SearchProviderName,
			MaxIterations:  r.cfg.MaxIterations,
			StartedAt:      started.Format(time.RFC3339),
		},
	}
	if question == "" {
		return result, fmt.Errorf("question is required")
	}

	agent, err := r.buildAgent(ctx)
	if err != nil {
		return result, err
	}
	iter := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent}).Query(ctx, question)
	var last string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return result, event.Err
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			msg, err := event.Output.MessageOutput.GetMessage()
			if err == nil && msg != nil && msg.Role == schema.Assistant {
				switch event.AgentName {
				case "planner":
					var plan ResearchPlan
					if json.Unmarshal([]byte(msg.Content), &plan) == nil {
						result.Plan = plan
					}
				case "parallel_executor":
					var step StepExecution
					if json.Unmarshal([]byte(msg.Content), &step) == nil {
						result.ExecutedSteps = append(result.ExecutedSteps, step)
						result.Sources = search.Deduplicate(append(result.Sources, step.Sources...))
					}
				default:
					last = msg.Content
				}
			}
		}
	}
	result.Answer.Markdown = last
	result.Answer.Summary = last
	completed := time.Now()
	result.Metadata.CompletedAt = completed.Format(time.RFC3339)
	result.Metadata.DurationMS = completed.Sub(started).Milliseconds()
	return result, nil
}

func (r *Runner) buildAgent(ctx context.Context) (adk.ResumableAgent, error) {
	planner, err := planexecute.NewPlanner(ctx, &planexecute.PlannerConfig{
		ToolCallingChatModel: r.cfg.Model,
		NewPlan: func(context.Context) planexecute.Plan {
			return &ResearchPlan{}
		},
	})
	if err != nil {
		return nil, err
	}
	replanner, err := planexecute.NewReplanner(ctx, &planexecute.ReplannerConfig{
		ChatModel: r.cfg.Model,
		NewPlan: func(context.Context) planexecute.Plan {
			return &ResearchPlan{}
		},
	})
	if err != nil {
		return nil, err
	}
	executor := NewEinoParallelExecutor(r.cfg)
	return planexecute.New(ctx, &planexecute.Config{
		Planner:       planner,
		Executor:      executor,
		Replanner:     replanner,
		MaxIterations: r.cfg.MaxIterations,
	})
}
```

- [ ] **Step 7: Add Eino executor adapter**

Append to `internal/research/executor.go`:

```go
const ResearchExecutedStepsSessionKey = "research_executed_steps"

type EinoParallelExecutor struct {
	cfg RunnerConfig
}

func NewEinoParallelExecutor(cfg RunnerConfig) *EinoParallelExecutor {
	return &EinoParallelExecutor{cfg: cfg}
}

func (e *EinoParallelExecutor) Name(context.Context) string {
	return "parallel_executor"
}

func (e *EinoParallelExecutor) Description(context.Context) string {
	return "executes a research step with parallel researchers"
}

func (e *EinoParallelExecutor) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer gen.Close()
		execution, err := e.run(ctx)
		if err != nil {
			gen.Send(&adk.AgentEvent{Err: err})
			return
		}
		b, err := json.Marshal(execution)
		if err != nil {
			gen.Send(&adk.AgentEvent{Err: err})
			return
		}
		adk.AddSessionValue(ctx, planexecute.ExecutedStepSessionKey, string(b))
		appendResearchStep(ctx, execution)
		gen.Send(adk.EventFromMessage(schema.AssistantMessage(string(b), nil), nil, schema.Assistant, ""))
	}()
	return iter
}

func (e *EinoParallelExecutor) run(ctx context.Context) (StepExecution, error) {
	planValue, ok := adk.GetSessionValue(ctx, planexecute.PlanSessionKey)
	if !ok {
		return StepExecution{}, fmt.Errorf("plan session value is missing")
	}
	plan, ok := planValue.(*ResearchPlan)
	if !ok {
		return StepExecution{}, fmt.Errorf("plan session value has type %T, want *ResearchPlan", planValue)
	}
	first := plan.FirstStep()
	if first == "" {
		return StepExecution{}, fmt.Errorf("research plan has no step to execute")
	}
	var step ResearchStep
	if err := json.Unmarshal([]byte(first), &step); err != nil {
		return StepExecution{}, fmt.Errorf("decode first research step: %w", err)
	}

	userInputValue, _ := adk.GetSessionValue(ctx, planexecute.UserInputSessionKey)
	question := formatUserInput(userInputValue)

	searchTool, err := NewWebSearchTool(e.cfg.SearchProvider, SearchLimits{
		MaxSearchesPerStep: e.cfg.MaxSearchesPerStep,
		ResultsPerSearch:   e.cfg.ResultsPerSearch,
	})
	if err != nil {
		return StepExecution{}, err
	}

	researchers, err := e.buildResearchers(ctx, searchTool)
	if err != nil {
		return StepExecution{}, err
	}
	core := NewParallelStepExecutor(researchers, NewAgentSynthesizer(e.cfg.Model))
	return core.ExecuteStep(ctx, StepExecutionInput{
		Question:      question,
		Step:          step,
		ExecutedSteps: getResearchSteps(ctx),
	})
}

func (e *EinoParallelExecutor) buildResearchers(ctx context.Context, searchTool tool.BaseTool) ([]Researcher, error) {
	roles := DefaultResearcherRoles()
	researchers := make([]Researcher, 0, len(roles))
	for i, role := range roles {
		researcher, err := NewAgentResearcher(ctx, role, focusForIndex(i), e.cfg.Model, searchTool)
		if err != nil {
			return nil, err
		}
		researchers = append(researchers, researcher)
	}
	return researchers, nil
}

func appendResearchStep(ctx context.Context, step StepExecution) {
	steps := getResearchSteps(ctx)
	steps = append(steps, step)
	adk.AddSessionValue(ctx, ResearchExecutedStepsSessionKey, steps)
}

func getResearchSteps(ctx context.Context) []StepExecution {
	value, ok := adk.GetSessionValue(ctx, ResearchExecutedStepsSessionKey)
	if !ok {
		return nil
	}
	steps, _ := value.([]StepExecution)
	return steps
}

func formatUserInput(value any) string {
	msgs, ok := value.([]adk.Message)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, msg := range msgs {
		if msg != nil && msg.Content != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(msg.Content)
		}
	}
	return sb.String()
}
```

Also add these imports to `internal/research/executor.go`:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/components/tool"
)
```

- [ ] **Step 8: Run compile tests**

Run:

```bash
go test ./internal/research
```

Expected: PASS.

- [ ] **Step 9: Commit**

Run:

```bash
git add internal/research go.mod go.sum
git commit -m "feat: assemble eino research runner"
```

Expected: commit succeeds.

## Task 9: CLI Wiring

**Files:**
- Modify: `cmd/research/main.go`
- Create: `README.md`

- [ ] **Step 1: Replace bootstrap with CLI parsing**

Replace `cmd/research/main.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	appconfig "github.com/hu-quan-er/eino_research/internal/config"
	"github.com/hu-quan-er/eino_research/internal/render"
	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("research", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "research.yaml", "path to config file")
	jsonOutput := fs.Bool("json", false, "output JSON")
	provider := fs.String("provider", "", "search provider: mock or google")
	maxIterations := fs.Int("max-iterations", 0, "maximum plan-execute-replan iterations")
	verbose := fs.Bool("verbose", false, "print progress to stderr")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: research [flags] \"question\"")
		return 2
	}

	format := ""
	if *jsonOutput {
		format = "json"
	}
	cfg, err := appconfig.Load(appconfig.LoadOptions{
		Path:     *configPath,
		Explicit: *configPath != "research.yaml",
		Overrides: appconfig.Overrides{
			Provider:      *provider,
			OutputFormat:  format,
			MaxIterations: *maxIterations,
			Verbose:       verbose,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Model.Timeout)
	defer cancel()

	var sp search.Provider
	searchName := cfg.Search.Provider
	switch cfg.Search.Provider {
	case "mock":
		sp = search.NewMockProvider()
	case "google":
		sp = search.NewGoogleProvider(search.GoogleConfig{
			APIKey: cfg.Search.Google.APIKey,
			CSEID:  cfg.Search.Google.CSEID,
		})
	default:
		fmt.Fprintf(os.Stderr, "unsupported provider: %s\n", cfg.Search.Provider)
		return 2
	}

	model, err := research.NewOpenAICompatibleModel(ctx, research.ModelConfig{
		APIKey:  cfg.Model.APIKey,
		Model:   cfg.Model.Model,
		BaseURL: cfg.Model.BaseURL,
		Timeout: cfg.Model.Timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "model error: %v\n", err)
		return 2
	}

	if cfg.Output.Verbose {
		fmt.Fprintf(os.Stderr, "running research with provider=%s max_iterations=%d timeout=%s\n", searchName, cfg.Research.MaxIterations, cfg.Model.Timeout.Round(time.Second))
	}

	runner, err := research.NewRunner(research.RunnerConfig{
		Model:              model,
		SearchProvider:     sp,
		ModelName:          cfg.Model.Model,
		SearchProviderName: searchName,
		MaxIterations:      cfg.Research.MaxIterations,
		MaxSearchesPerStep: cfg.Search.MaxSearchesPerStep,
		ResultsPerSearch:   cfg.Search.ResultsPerSearch,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner error: %v\n", err)
		return 2
	}

	result, err := runner.Run(ctx, fs.Arg(0))
	if err != nil {
		result.Error = &research.RunError{Stage: "run", Message: err.Error()}
		if cfg.Output.Format == "json" {
			out, _ := render.JSON(result)
			fmt.Print(out)
		} else {
			fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		}
		return 1
	}

	if cfg.Output.Format == "json" {
		out, err := render.JSON(result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render error: %v\n", err)
			return 1
		}
		fmt.Print(out)
		return 0
	}

	fmt.Print(render.Markdown(result))
	return 0
}
```

- [ ] **Step 2: Run CLI build**

Run:

```bash
go test ./...
go run ./cmd/research --help
```

Expected: tests pass, help prints flag usage and exits with status 0.

- [ ] **Step 3: Create README**

Create `README.md`:

```markdown
# Eino Research Agent

Go CLI Deep Research Agent based on Eino.

## Run

```bash
go run ./cmd/research --provider mock "Eino 适合构建 research agent 吗？"
```

## Configuration

Copy `research.example.yaml` to `research.yaml` and edit non-secret defaults.
Prefer environment variables for API keys:

```bash
export OPENAI_API_KEY=<openai-compatible-api-key>
export OPENAI_MODEL=gpt-4.1
export GOOGLE_API_KEY=<google-api-key>
export GOOGLE_CSE_ID=<google-cse-id>
```

## Output

Markdown is the default. Use `--json` for structured research process output.
```

- [ ] **Step 4: Commit**

Run:

```bash
git add cmd/research/main.go README.md
git commit -m "feat: add research cli"
```

Expected: commit succeeds.

## Task 10: Final Verification

**Files:**
- Modify: generated `go.sum` if dependency resolution changes

- [ ] **Step 1: Format all Go code**

Run:

```bash
gofmt -w cmd internal
```

Expected: command exits 0.

- [ ] **Step 2: Tidy module**

Run:

```bash
go mod tidy
```

Expected: command exits 0 and `go.mod`/`go.sum` contain only required dependencies.

- [ ] **Step 3: Run full unit tests**

Run:

```bash
go test ./...
```

Expected: PASS for all packages.

- [ ] **Step 4: Run CLI help smoke test**

Run:

```bash
go run ./cmd/research --help
```

Expected: command prints flag usage and exits 0.

- [ ] **Step 5: Commit verification fixes**

Run:

```bash
git status --short
git add go.mod go.sum cmd internal README.md research.example.yaml
git commit -m "test: verify research agent implementation"
```

Expected: commit succeeds if formatting or tidy changed files. If `git status --short` is empty, skip this commit.

## Self-Review

Spec coverage:

- CLI is covered by Task 9.
- Config file, env, and flag precedence are covered by Task 2 and Task 9.
- Mock and Google search providers are covered by Task 3.
- `ResearchPlan` and JSON output contract are covered by Task 4 and Task 5.
- `web_search` tool and search limits are covered by Task 6.
- Parallel researcher execution is covered by Task 7.
- Eino planexecute runner assembly is covered by Task 8.
- Final verification is covered by Task 10.

No spec coverage gaps are intentionally left open in this plan.
