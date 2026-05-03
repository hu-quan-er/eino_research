package config

import (
	"bytes"
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
}

type SearchConfig struct {
	Provider           string             `yaml:"provider"`
	Google             GoogleSearchConfig `yaml:"google"`
	MaxSearchesPerStep int                `yaml:"max_searches_per_step"`
	ResultsPerSearch   int                `yaml:"results_per_search"`
}

type GoogleSearchConfig struct {
	APIKey string `yaml:"api_key"`
	CSEID  string `yaml:"cse_id"`
}

type ResearchConfig struct {
	MaxIterations   int      `yaml:"max_iterations"`
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
	MaxIterations *int
	Verbose       *bool
}

func Defaults() Config {
	return Config{
		Model: ModelConfig{
			Provider: "openai-compatible",
			Timeout:  60 * time.Second,
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
		} else if err := unmarshalStrict(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", opts.Path, err)
		}
	}
	applyEnv(&cfg)
	applyOverrides(&cfg, opts.Overrides)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func unmarshalStrict(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(out)
}

func (m *ModelConfig) UnmarshalYAML(value *yaml.Node) error {
	allowed := map[string]struct{}{
		"provider": {},
		"api_key":  {},
		"model":    {},
		"base_url": {},
		"timeout":  {},
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown model field %q", key)
		}
	}

	type rawModelConfig struct {
		Provider string   `yaml:"provider"`
		APIKey   string   `yaml:"api_key"`
		Model    string   `yaml:"model"`
		BaseURL  string   `yaml:"base_url"`
		Timeout  Duration `yaml:"timeout"`
	}

	raw := rawModelConfig{
		Provider: m.Provider,
		APIKey:   m.APIKey,
		Model:    m.Model,
		BaseURL:  m.BaseURL,
		Timeout:  Duration{Duration: m.Timeout},
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	m.Provider = raw.Provider
	m.APIKey = raw.APIKey
	m.Model = raw.Model
	m.BaseURL = raw.BaseURL
	m.Timeout = raw.Timeout.Duration
	return nil
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
	if o.MaxIterations != nil {
		cfg.Research.MaxIterations = *o.MaxIterations
	}
	if o.Verbose != nil {
		cfg.Output.Verbose = *o.Verbose
	}
}

func (c Config) Validate() error {
	if c.Model.Provider != "openai-compatible" {
		return fmt.Errorf("unsupported model provider %q", c.Model.Provider)
	}
	if c.Model.Timeout <= 0 {
		return errors.New("model.timeout must be positive")
	}
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
