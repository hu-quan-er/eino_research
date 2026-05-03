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
