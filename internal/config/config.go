package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 包装 time.Duration，让 YAML 中可以直接写 "30s"、"2m" 这类
// Go duration 字符串。对外的 Config 仍保存解析后的 time.Duration，避免 YAML
// 解析细节污染业务代码。
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

// Config 是 cmd/research 使用的完整运行配置。
//
// 调用方应该优先使用 Load 获取 Config，而不是直接构造它；Load 会统一处理默认值、
// 配置文件、环境变量、命令行覆盖和最终校验。
type Config struct {
	Model    ModelConfig    `yaml:"model"`
	Search   SearchConfig   `yaml:"search"`
	Research ResearchConfig `yaml:"research"`
	Output   OutputConfig   `yaml:"output"`
}

// ModelConfig 描述 planner、researcher、synthesizer 共用的 OpenAI-compatible
// chat model 配置。
type ModelConfig struct {
	Provider string        `yaml:"provider"`
	APIKey   string        `yaml:"api_key"`
	Model    string        `yaml:"model"`
	BaseURL  string        `yaml:"base_url"`
	Timeout  time.Duration `yaml:"-"`
}

// SearchConfig 描述搜索 provider 以及单个 step/todo 内的搜索预算。
//
// ResultsPerSearch 还要受具体 provider 限制，例如 Google Custom Search 单次最多
// 允许 10 条结果。
type SearchConfig struct {
	Provider           string             `yaml:"provider"`
	Google             GoogleSearchConfig `yaml:"google"`
	MaxSearchesPerStep int                `yaml:"max_searches_per_step"`
	ResultsPerSearch   int                `yaml:"results_per_search"`
}

// GoogleSearchConfig 保存 Google Custom Search 需要的凭据。
type GoogleSearchConfig struct {
	APIKey string `yaml:"api_key"`
	CSEID  string `yaml:"cse_id"`
}

// ResearchConfig 控制外层 plan/execute 循环，以及 todo 内部的 bounded research
// 深挖循环。
type ResearchConfig struct {
	MaxIterations             int      `yaml:"max_iterations"`
	MaxResearchersPerTodo     int      `yaml:"max_researchers_per_todo"`
	MaxTodoResearchIterations int      `yaml:"max_todo_research_iterations"`
	ResearcherRoles           []string `yaml:"researcher_roles"`
}

// OutputConfig 控制最终 CLI 输出格式和进度日志。
type OutputConfig struct {
	Format  string `yaml:"format"`
	Verbose bool   `yaml:"verbose"`
}

// LoadOptions 描述 Load 如何处理配置文件和命令行覆盖。
//
// Explicit 为 true 时，配置文件缺失会被视为错误；否则默认配置文件不存在也允许继续，
// 方便首次运行和测试场景。
type LoadOptions struct {
	Path      string
	Explicit  bool
	Overrides Overrides
}

// Overrides 表示命令行 flag 提供的覆盖值，会在文件和环境变量之后应用。
//
// 指针字段用于区分“没有提供该 flag”和“显式提供了零值”。
type Overrides struct {
	Provider      string
	OutputFormat  string
	MaxIterations *int
	Verbose       *bool
}

// Defaults 返回保守默认值，使本地测试和示例在没有外部搜索凭据时也能运行。
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
			MaxIterations:             5,
			MaxResearchersPerTodo:     3,
			MaxTodoResearchIterations: 2,
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

// Load 读取配置文件，叠加环境变量与命令行覆盖，并在返回前执行完整校验。
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

// unmarshalStrict 会拒绝未知 YAML 字段，避免配置项拼写错误时静默回落到默认值。
func unmarshalStrict(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(out)
}

// UnmarshalYAML 让 model.timeout 在 YAML 中保持人类可读，同时在 ModelConfig 中
// 保存为原生 time.Duration。
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

// applyEnv 在 YAML 加载后叠加环境变量。
//
// 这样配置文件可以保存本地默认值，而 API Key 等敏感信息可以由 shell 或 CI 注入。
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

// applyOverrides 最后应用 CLI flag，保证用户在命令行中的显式意图优先于文件和环境变量。
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

// Validate 校验 YAML 解析本身无法表达的跨字段约束，例如 provider 专属凭据和结果数量限制。
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
		if c.Search.ResultsPerSearch > 10 {
			return errors.New("search.results_per_search must be <= 10 for google provider")
		}
	}
	if c.Output.Format != "markdown" && c.Output.Format != "json" {
		return fmt.Errorf("unsupported output format %q", c.Output.Format)
	}
	if c.Research.MaxIterations <= 0 {
		return errors.New("research.max_iterations must be positive")
	}
	if c.Research.MaxResearchersPerTodo <= 0 {
		return errors.New("research.max_researchers_per_todo must be positive")
	}
	if c.Research.MaxTodoResearchIterations <= 0 {
		return errors.New("research.max_todo_research_iterations must be positive")
	}
	if c.Search.MaxSearchesPerStep <= 0 {
		return errors.New("search.max_searches_per_step must be positive")
	}
	if c.Search.ResultsPerSearch <= 0 {
		return errors.New("search.results_per_search must be positive")
	}
	return nil
}
