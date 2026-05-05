package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

	result, err := runner.Run(ctx, question)
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

func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}
