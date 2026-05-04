# Eino Research Agent

Go CLI Deep Research Agent based on Eino.

## Run

```bash
go run ./cmd/research --provider mock "Eino 适合构建 research agent 吗？"
```

`mock` is a search provider only. It returns mock web search results, but it does
not mock the LLM/model. Actual runs still require a model API key and model name.

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
