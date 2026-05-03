# Eino Research Agent 设计文档

日期：2026-05-04

## 目标

在当前目录下实现一款基于 Eino 框架的 Deep Research CLI。第一版要能帮助用户熟悉 Eino 的复杂 agent 用法，而不是只做单轮搜索总结。

第一版核心目标：

- 提供 `research` CLI。
- 使用 Eino `prebuilt/planexecute` 作为外层 plan-execute-replan 编排。
- 使用自定义 `ResearchPlan` 表达结构化研究计划。
- 在 Executor 阶段引入三个固定并行 researcher。
- 支持 Mock Search 和 Google Custom Search。
- 支持 OpenAI-compatible 模型配置。
- 支持 `research.yaml` 配置文件、环境变量和 CLI flags。
- 默认输出 Markdown，`--json` 输出研究过程 JSON。
- 输出和内部 Eino 类型解耦，为后续 HTTP 服务保留复用边界。

## 非目标

第一版不实现：

- HTTP 服务。
- Web UI。
- 数据库持久化。
- 用户账号、多租户、任务队列。
- 长期记忆。
- 文件搜索、MCP、代码执行工具。
- CitationAgent 专用引用校验。
- Planner 动态生成 researcher roles。
- 多个 `ResearchStep` 同时并行执行。

第一版只在单个 `ResearchStep` 内并行运行 researcher；不同 step 之间仍按 plan-execute-replan 串行推进。

## 用户入口

CLI 示例：

```bash
research "研究问题"
research --config research.yaml "研究问题"
research --json "研究问题"
research --provider google --max-iterations 5 "研究问题"
```

输出规则：

- 默认输出 Markdown 到 stdout。
- `--json` 输出结构化研究过程 JSON 到 stdout。
- `--verbose` 输出简要进度到 stderr。
- 运行失败时返回非零退出码。

## 总体架构

```text
CLI
  -> Config Loader
  -> Model Factory
  -> Search Provider
  -> Research Runner

Research Runner
  -> Planner: 生成 ResearchPlan
  -> Loop:
      -> Parallel Executor:
          -> background_researcher
          -> evidence_researcher
          -> counterpoint_researcher
          -> synthesis step result
      -> Replanner: 判断完成或继续
  -> Result Collector
  -> Markdown / JSON Renderer
```

外层编排复用 Eino `adk/prebuilt/planexecute`。自定义部分集中在：

- `ResearchPlan`：替代默认 `steps []string`。
- Parallel Executor：替代默认 `planexecute.NewExecutor`。
- `web_search` tool：把搜索 provider 暴露给 researcher。
- Result Collector：把 Eino session 和 agent 输出归一化成 `ResearchResult`。
- Renderer：从 `ResearchResult` 渲染 Markdown 或 JSON。

## Eino 使用方式

第一版使用 Eino v0.8.13 的以下能力：

- `adk.NewChatModelAgent`：构建 researcher 和 synthesis agent。
- `adk.NewParallelAgent`：在单个 step 内并行运行三个 researcher。
- `adk.NewSequentialAgent` 和 `adk.NewLoopAgent`：通过 `prebuilt/planexecute.New` 间接使用。
- `adk/prebuilt/planexecute.NewPlanner`：生成结构化 `ResearchPlan`。
- `adk/prebuilt/planexecute.NewReplanner`：根据已执行步骤决定完成或继续。
- `components/tool/utils.InferTool`：创建 `web_search` 工具。

`planexecute.New` 接收自定义 Planner、Executor、Replanner。第一版保留 Planner/Replanner 的 Eino 预构建路径，传入自定义 Parallel Executor。

## ResearchPlan

`ResearchPlan` 实现 Eino `planexecute.Plan` 接口。

```go
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
```

行为约定：

- `FirstStep()` 返回当前第一个 `ResearchStep` 的 JSON 字符串。
- `MarshalJSON()` 和 `UnmarshalJSON()` 稳定处理完整计划。
- 空计划的 `FirstStep()` 返回空字符串。
- Planner/Replanner 输出的 `steps` 表示剩余待执行步骤。
- 已完成步骤由 `ExecutedStepsSessionKey` 和应用层 `ResearchResult.ExecutedSteps` 维护。

`research_axes` 第一版可由 Planner 输出，也可为空。它用于记录当前步骤希望覆盖的研究视角，并为后续升级到动态 researcher specs 预留空间。

## 并行 Executor

Parallel Executor 是自定义 `adk.Agent`，负责执行当前 `ResearchStep`。

执行流程：

1. 从 Eino session 读取 `planexecute.PlanSessionKey`、`UserInputSessionKey`、`ExecutedStepsSessionKey`。
2. 从 `ResearchPlan.FirstStep()` 得到当前 `ResearchStep`。
3. 构造三个 researcher 的输入。
4. 使用 `adk.NewParallelAgent` 并行运行三个 researcher。
5. 收集 researcher 输出和错误。
6. 调用 synthesis agent 合并三路结果。
7. 将合并后的 step result 写入 `planexecute.ExecutedStepSessionKey`。
8. 发送 agent event，让 Replanner 使用 Eino 预构建流程继续判断。

三个固定 researcher：

```text
background_researcher
  关注：背景、定义、上下文、时间线、关键概念
  目标：让后续结论不缺上下文

evidence_researcher
  关注：数据、事实、案例、权威来源、主流观点
  目标：为结论提供可引用证据

counterpoint_researcher
  关注：反例、争议、限制、失败案例、不同意见
  目标：降低确认偏误，暴露不确定性
```

每个 researcher 都是 Eino `ChatModelAgent`，共享 `web_search` 工具，但 instruction 和 focus 不同。

限额：

- 每个 researcher 每个 step 最多调用搜索 2 次。
- 每次搜索默认返回 5 条结果。
- 每个 step 最多 6 次搜索。
- plan-execute-replan 默认最大循环次数为 5。
- 整个 CLI run 默认超时 60 秒。

## Synthesis

Synthesis agent 不进行搜索，只合并当前 step 的并行研究结果。

输入：

- 用户原始问题。
- 当前 `ResearchStep`。
- 已执行步骤摘要。
- 三个 `ResearcherResult`。

输出：

- 当前 step 的摘要。
- 关键发现。
- 不确定性、缺口和限制。
- 归一化来源列表。

Synthesis 输出会被序列化为 `StepExecution`，同时作为字符串写入 `planexecute.ExecutedStepSessionKey`，供 Replanner 继续判断。

## 搜索 Provider 和 web_search 工具

搜索 provider 不依赖 Eino：

```go
type Provider interface {
    Search(ctx context.Context, query string, limit int) ([]Source, error)
}
```

第一版 provider：

- `mock`：返回稳定测试数据。
- `google`：调用 Google Custom Search JSON API。

`web_search` 是 Eino tool wrapper，负责：

- 接收 query 和 limit。
- 检查每个 step 的搜索次数限制。
- 调用当前 provider。
- 返回统一来源 JSON。
- 在错误中包含 provider、query 和原因。

## 配置

第一版支持配置文件、环境变量和 CLI flags。

优先级：

```text
CLI flags > 环境变量 > 配置文件 > 默认值
```

默认读取当前目录 `research.yaml`。如果默认文件不存在，不报错。显式传入 `--config path/to/config.yaml` 时，如果文件不存在或解析失败，启动失败。

配置示例文件为 `research.example.yaml`：

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

环境变量：

```text
OPENAI_API_KEY
OPENAI_MODEL
OPENAI_BASE_URL
GOOGLE_API_KEY
GOOGLE_CSE_ID
```

配置文件可以包含非敏感默认值。示例文件不包含真实 API key。

## JSON 输出契约

`--json` 输出研究过程 JSON。

应用层数据结构：

```go
type ResearchResult struct {
    Question      string          `json:"question"`
    Answer        Answer          `json:"answer"`
    Plan          ResearchPlan    `json:"plan"`
    ExecutedSteps []StepExecution `json:"executed_steps"`
    Sources       []Source        `json:"sources"`
    Metadata      Metadata        `json:"metadata"`
    Error         *RunError       `json:"error,omitempty"`
}

type Answer struct {
    Markdown     string   `json:"markdown"`
    Summary      string   `json:"summary"`
    KeyFindings  []string `json:"key_findings"`
    Limitations  []string `json:"limitations"`
}

type StepExecution struct {
    Step              ResearchStep       `json:"step"`
    ResearcherResults []ResearcherResult `json:"researcher_results"`
    Summary           string             `json:"summary"`
    Gaps              []string           `json:"gaps,omitempty"`
    Sources           []Source           `json:"sources"`
}

type ResearcherResult struct {
    Role     string    `json:"role"`
    Focus    string    `json:"focus"`
    Queries  []string  `json:"queries"`
    Findings []Finding `json:"findings"`
    Sources  []Source  `json:"sources"`
    Errors   []string  `json:"errors,omitempty"`
}

type Finding struct {
    Claim     string   `json:"claim"`
    Rationale string   `json:"rationale"`
    SourceIDs []string `json:"source_ids"`
}

type Source struct {
    ID       string `json:"id"`
    Title    string `json:"title"`
    URL      string `json:"url"`
    Snippet  string `json:"snippet"`
    Provider string `json:"provider"`
    Query    string `json:"query"`
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

```json
{
  "question": "Eino 框架适合构建 Deep Research Agent 吗？",
  "answer": {
    "markdown": "# 结论\n\nEino 适合构建 Go 语言 Deep Research Agent 的第一版原型。",
    "summary": "Eino 的 ADK、tool calling 和 plan-execute-replan 能覆盖第一版所需的复杂 agent 编排。",
    "key_findings": [
      "Eino 提供 ChatModelAgent、ParallelAgent 和 LoopAgent 等 ADK 构件。",
      "prebuilt/planexecute 可复用 Planner 和 Replanner，降低第一版编排风险。"
    ],
    "limitations": [
      "第一版不做长期记忆和 CitationAgent。",
      "多个 ResearchStep 之间仍串行执行。"
    ]
  },
  "plan": {
    "steps": [
      {
        "id": "step_1",
        "title": "评估 Eino ADK 能力",
        "question": "Eino ADK 提供哪些构建 Deep Research Agent 的基础能力？",
        "search_queries": ["Eino ADK ChatModelAgent ParallelAgent planexecute"],
        "research_axes": ["background", "evidence", "counterpoint"],
        "success_criteria": ["列出相关 ADK 构件", "说明这些构件如何组合"]
      }
    ]
  },
  "executed_steps": [
    {
      "step": {
        "id": "step_1",
        "title": "评估 Eino ADK 能力",
        "question": "Eino ADK 提供哪些构建 Deep Research Agent 的基础能力？",
        "search_queries": ["Eino ADK ChatModelAgent ParallelAgent planexecute"],
        "research_axes": ["background", "evidence", "counterpoint"],
        "success_criteria": ["列出相关 ADK 构件", "说明这些构件如何组合"]
      },
      "researcher_results": [],
      "summary": "Eino ADK 提供构建第一版 Deep Research Agent 所需的基础编排能力。",
      "sources": []
    }
  ],
  "sources": [
    {
      "id": "src_1",
      "title": "Eino README",
      "url": "https://github.com/cloudwego/eino",
      "snippet": "Eino provides ADK, tool calling, and orchestration components.",
      "provider": "google",
      "query": "Eino ADK ChatModelAgent ParallelAgent planexecute"
    }
  ],
  "metadata": {
    "model": "gpt-4.1",
    "search_provider": "google",
    "max_iterations": 5,
    "started_at": "2026-05-04T00:00:00+08:00",
    "completed_at": "2026-05-04T00:00:01+08:00",
    "duration_ms": 1000
  }
}
```

Markdown 和 JSON 都从 `ResearchResult` 渲染，避免两套输出逻辑不一致。

## 错误处理

配置错误：

- YAML 解析失败：启动前失败，指出文件路径和字段。
- 显式 `--config` 文件不存在：启动前失败。
- provider 为 google 但缺少 Google key 或 CSE ID：启动前失败。
- CLI flag 值非法：启动前失败。

运行错误：

- 单个 researcher 失败：记录到 `ResearcherResult.Errors`，synthesis 基于剩余结果继续。
- 三个 researcher 都失败：当前 step 失败，整个 run 返回非零退出码。
- 搜索 provider 失败：错误包含 provider、query、HTTP 状态或底层原因。
- 模型调用失败：错误包含阶段，如 planner、researcher、synthesis、replanner。
- 超时：取消 context，Markdown 模式向 stderr 输出错误；JSON 模式包含 `error` 字段和已知 metadata。

## 文件结构

```text
.
├── cmd/research/
│   └── main.go
├── internal/config/
│   ├── config.go
│   └── config_test.go
├── internal/search/
│   ├── provider.go
│   ├── mock.go
│   ├── google.go
│   └── *_test.go
├── internal/research/
│   ├── plan.go
│   ├── result.go
│   ├── runner.go
│   ├── executor.go
│   ├── researchers.go
│   ├── tools.go
│   └── *_test.go
├── internal/render/
│   ├── markdown.go
│   ├── json.go
│   └── *_test.go
├── docs/discussion/
├── docs/superpowers/specs/
├── research.example.yaml
├── README.md
├── go.mod
└── go.sum
```

模块职责：

- `cmd/research`：CLI 参数解析、调用 config loader、运行 runner、输出结果、处理退出码。
- `internal/config`：加载 `research.yaml`、env、CLI overrides，合并最终配置。
- `internal/search`：定义 Provider，实现 Mock 和 Google Custom Search。
- `internal/research`：封装 Eino 使用，包括 plan、executor、researchers、tools、runner。
- `internal/render`：渲染 Markdown 和 JSON。
- `docs/discussion`：维护中文讨论记录。
- `docs/superpowers/specs`：维护正式设计文档。

Eino 依赖集中在 `internal/research`。CLI、config、search 和 render 不直接依赖 Eino 类型。

## 测试策略

默认测试不依赖真实 OpenAI 或 Google。

测试分层：

- `config`：默认值、配置文件、env 覆盖、CLI 覆盖、非法配置。
- `search`：mock provider、google 请求构造、google 响应解析、HTTP 错误、source 去重。
- `plan`：`ResearchPlan` 实现 `planexecute.Plan`、JSON marshal/unmarshal、空 plan。
- `tools`：`web_search` schema、limit、provider 错误。
- `executor`：并行 researcher、单个失败继续、全部失败中断、写入 `ExecutedStepSessionKey`。
- `render`：Markdown 内容、JSON 契约、错误 JSON。

命令：

```bash
go test ./...
RUN_INTEGRATION=1 go test ./... -run Integration
```

Integration 测试默认不跑，只有设置 `RUN_INTEGRATION=1` 且提供真实 key 时才运行。

## 后续演进

第一版稳定后，可以按以下方向迭代：

- HTTP 服务。
- Planner 动态生成 researcher specs。
- 真正并行执行多个互相独立的 `ResearchStep`。
- CitationAgent 引用校验。
- 文件搜索、MCP、代码执行工具。
- 自定义 ADK Workflow，逐步替换 `prebuilt/planexecute`。
- 任务持久化和异步执行。
