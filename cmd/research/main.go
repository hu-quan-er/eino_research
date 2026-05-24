package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	appconfig "github.com/hu-quan-er/eino_research/internal/config"
	"github.com/hu-quan-er/eino_research/internal/render"
	"github.com/hu-quan-er/eino_research/internal/research"
	"github.com/hu-quan-er/eino_research/internal/search"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

var (
	// newOpenAICompatibleModel 和 stdinIsInteractive 是测试替换点，避免 CLI 测试真的创建模型或
	// 依赖真实终端。
	newOpenAICompatibleModel = research.NewOpenAICompatibleModel
	stdinIsInteractive       = isInteractiveStdin
)

// run 是 CLI 的可测试入口。
//
// 它负责解析 flag、加载配置、创建 provider/model/runner，并根据 --plan-only、--yes、
// output.format 等选项决定只预览 plan 还是完整执行。
func run(args []string) int {
	fs := flag.NewFlagSet("research", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: research [flags] \"question\"")
		fs.PrintDefaults()
	}

	configPath := fs.String("config", "research.yaml", "path to config file")
	jsonOutput := fs.Bool("json", false, "output JSON")
	provider := fs.String("provider", "", "search provider: mock or google")
	maxIterations := fs.Int("max-iterations", 0, "maximum plan-execute-replan iterations")
	maxParallel := fs.Int("max-parallel", 0, "maximum runnable todos to execute concurrently")
	yes := fs.Bool("yes", false, "skip confirmation and execute the generated plan")
	planOnly := fs.Bool("plan-only", false, "generate and print the plan without executing")
	planJSON := fs.Bool("plan-json", false, "with --plan-only, print ResearchTodoPlan JSON")
	verbose := fs.Bool("verbose", false, "print progress to stderr")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: research [flags] \"question\"")
		return 2
	}
	question := strings.TrimSpace(fs.Arg(0))
	if question == "" {
		fmt.Fprintln(os.Stderr, "question is required")
		return 2
	}

	format := ""
	if *jsonOutput {
		format = "json"
	}

	var maxIterationsOverride *int
	if flagProvided(fs, "max-iterations") {
		maxIterationsOverride = maxIterations
	}

	var verboseOverride *bool
	if flagProvided(fs, "verbose") {
		verboseOverride = verbose
	}

	cfg, err := appconfig.Load(appconfig.LoadOptions{
		Path:     *configPath,
		Explicit: flagProvided(fs, "config"),
		Overrides: appconfig.Overrides{
			Provider:      *provider,
			OutputFormat:  format,
			MaxIterations: maxIterationsOverride,
			Verbose:       verboseOverride,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 2
	}
	if *planJSON && !*planOnly {
		fmt.Fprintln(os.Stderr, "--plan-json requires --plan-only")
		return 2
	}
	if !*yes && !*planOnly && !stdinIsInteractive() {
		fmt.Fprintln(os.Stderr, "non-interactive execution requires --yes or --plan-only")
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

	model, err := newOpenAICompatibleModel(ctx, research.ModelConfig{
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
		Model:                     model,
		SearchProvider:            sp,
		ModelName:                 cfg.Model.Model,
		SearchProviderName:        searchName,
		MaxIterations:             cfg.Research.MaxIterations,
		MaxSearchesPerStep:        cfg.Search.MaxSearchesPerStep,
		ResultsPerSearch:          cfg.Search.ResultsPerSearch,
		MaxParallelTodos:          *maxParallel,
		MaxResearchersPerTodo:     cfg.Research.MaxResearchersPerTodo,
		MaxTodoResearchIterations: cfg.Research.MaxTodoResearchIterations,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner error: %v\n", err)
		return 2
	}

	plan, err := runner.Plan(ctx, question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan error: %v\n", err)
		return 1
	}

	if *planOnly {
		if *planJSON {
			out, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "render plan error: %v\n", err)
				return 1
			}
			fmt.Println(string(out))
			return 0
		}
		fmt.Print(renderTodoPlanPreview(plan))
		return 0
	}

	fmt.Fprint(os.Stderr, renderTodoPlanPreview(plan))
	if !*yes {
		confirmed, err := confirmPlanExecution()
		if err != nil {
			fmt.Fprintf(os.Stderr, "confirmation error: %v\n", err)
			return 1
		}
		if !confirmed {
			return 0
		}
	}

	result, err := runner.Execute(ctx, question, plan)
	if err != nil {
		if result.Error == nil {
			result.Error = &research.RunError{Stage: "run", Message: err.Error()}
		}
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

// flagProvided 判断某个 flag 是否由用户显式提供，用于区分默认值和命令行覆盖。
func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

// isInteractiveStdin 判断当前 stdin 是否是终端。
//
// 非交互环境必须显式使用 --yes 或 --plan-only，避免脚本误触发长时间执行。
func isInteractiveStdin() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// confirmPlanExecution 在交互模式下向用户确认是否执行 planner 生成的 todo plan。
func confirmPlanExecution() (bool, error) {
	fmt.Fprint(os.Stderr, "Continue and execute this plan? [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// renderTodoPlanPreview 把 ResearchTodoPlan 渲染为人类可审阅的执行预览。
func renderTodoPlanPreview(plan research.ResearchTodoPlan) string {
	var sb strings.Builder
	sb.WriteString("Objective: ")
	sb.WriteString(plan.Objective)
	sb.WriteString("\n\n")
	for _, section := range plan.Sections {
		sb.WriteString("## ")
		sb.WriteString(section.Title)
		if section.Description != "" {
			sb.WriteString("\n")
			sb.WriteString(section.Description)
		}
		sb.WriteString("\n\n")
		for _, todo := range plan.Todos {
			if todo.SectionID != section.ID {
				continue
			}
			sb.WriteString("- ")
			sb.WriteString(todo.ID)
			sb.WriteString(": ")
			sb.WriteString(todo.Title)
			if len(todo.DependsOn) > 0 {
				sb.WriteString(" (depends on: ")
				sb.WriteString(strings.Join(todo.DependsOn, ", "))
				sb.WriteString(")")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
