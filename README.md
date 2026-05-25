# Eino Research Agent

基于 Eino 的 Go CLI Deep Research Agent。当前主流程已经收敛为 `ResearchTodoPlan` 驱动的 plan-and-execute 架构：Planner 负责把问题拆成结构化 todo plan，Scheduler 负责按依赖调度 todo，单个 todo 内部再派发多个 researcher 子代理并行研究，最后聚合为可渲染的 `ResearchResult`。

## 快速运行

```bash
go run ./cmd/research --provider mock --yes "Eino 适合构建 research agent 吗？"
```

`mock` 只是搜索 provider，会返回模拟搜索结果；模型调用仍然是真实的 OpenAI-compatible chat model，因此需要配置模型 API key 和模型名。

只生成并预览计划，不执行研究：

```bash
go run ./cmd/research --provider mock --plan-only "Eino 适合构建 research agent 吗？"
```

输出结构化 JSON：

```bash
go run ./cmd/research --provider mock --yes --json "Eino 适合构建 research agent 吗？"
```

## 配置

复制 `research.example.yaml` 为 `research.yaml`，把非敏感默认值写入配置文件。密钥建议使用环境变量注入：

```bash
export OPENAI_API_KEY=<openai-compatible-api-key>
export OPENAI_MODEL=gpt-4.1
export GOOGLE_API_KEY=<google-api-key>
export GOOGLE_CSE_ID=<google-cse-id>
```

核心配置项：

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
  max_researchers_per_todo: 3
  max_todo_research_iterations: 2

output:
  format: markdown
  verbose: false
```

## 当前架构

当前项目采用一条明确的 todo-plan 主路径，不再保留早期 `ResearchPlan` / Eino `prebuilt/planexecute` legacy 路径。

```text
cmd/research
  |
  v
Runner.Plan(question)
  |
  v
ResearchTodoPlan
  |
  v
Runner.Execute(question, plan)
  |
  v
TodoScheduler
  |
  v
executeTodo
  |
  +--> TodoDispatcher
  |      |
  |      v
  |   TodoResearchJob[]
  |
  +--> AgentResearcher[] + web_search/web_fetch
  |      |
  |      v
  |   ResearcherResult[]
  |
  +--> AgentSynthesizer
  |      |
  |      v
  |   StepExecution
  |
  v
TodoExecution[]
  |
  v
ResearchResult
  |
  v
render.Markdown / render.JSON
```

### 模块职责

`cmd/research`

- 解析 CLI flags。
- 加载配置、环境变量和命令行覆盖项。
- 创建搜索 provider、OpenAI-compatible model 和 `Runner`。
- 默认先展示 planner 生成的 todo plan，再由用户确认执行；非交互环境需要 `--yes` 或 `--plan-only`。

`internal/config`

- 负责默认值、YAML、环境变量和 CLI overrides 的合并。
- 使用 strict YAML decode，未知字段会报错，避免配置拼写错误静默回落。
- 当前 research 预算集中在 todo fan-out 和 todo 内部 gap retry。

`internal/search`

- 定义搜索 provider 抽象。
- 当前支持 `mock` 和 `google`。
- `web_search` 工具会通过该 provider 获取 `search.Source`。

`internal/research`

- 核心 workflow 所在模块。
- 包含 planner、todo plan validation、todo scheduler、todo dispatcher、researcher、synthesizer、工具封装、证据归一化和结果模型。

`internal/render`

- 把 `ResearchResult` 渲染为 Markdown 或 JSON。
- Markdown 会追加执行摘要、findings/evidence 和 sources。

## 数据流

### 1. CLI 输入到 Runner

用户问题从 CLI 进入后，`cmd/research` 创建：

- `search.Provider`：`mock` 或 `google`。
- `model.ToolCallingChatModel`：OpenAI-compatible chat model。
- `research.Runner`：持有模型、搜索 provider、预算和可替换组件。

CLI 默认使用显式两阶段流程：

1. `runner.Plan(ctx, question)` 生成计划。
2. 预览 `ResearchTodoPlan`。
3. 用户确认后调用 `runner.Execute(ctx, question, plan)`。

库调用方也可以直接使用 `runner.Run(ctx, question)`，它等价于 `Plan + Execute`。

### 2. Planner 生成 ResearchTodoPlan

Planner 输出的主数据结构是 `ResearchTodoPlan`：

```json
{
  "objective": "研究目标",
  "sections": [
    {"id": "background", "title": "Background", "description": "可选说明"}
  ],
  "todos": [
    {
      "id": "todo_background",
      "section_id": "background",
      "title": "Clarify background",
      "question": "当前 todo 要回答的问题",
      "search_queries": ["初始搜索 query"],
      "acceptance_criteria": ["验收标准"],
      "depends_on": ["前置 todo id"]
    }
  ]
}
```

Planner 可靠性由三层保护组成：

- 优先使用强制 tool call：`create_research_todo_plan`。
- tool call 不可用或返回非法内容时，进入文本 JSON repair 流程，最多尝试 3 轮。
- 每次解析后都会执行结构校验和质量 lint。

结构校验负责：

- 必填字段。
- section/todo ID 去重。
- todo.section_id 引用合法性。
- depends_on 引用合法性。
- 依赖图无环。

质量 lint 负责：

- plan 粒度是否过粗或过细。
- evidence-oriented todo 是否有搜索 query。
- todo 是否包含 acceptance criteria。
- section/todo 组织是否符合当前 research 预期。

### 3. Execute 校验和调度

`Runner.Execute` 会再次校验传入 plan：

- `plan.Validate()`：结构校验。
- `validateResearchTodoPlanQuality(plan)`：质量校验。

这样可以避免外部传入、缓存恢复或人工修改后的低质量 plan 被直接执行。

校验通过后，`TodoScheduler` 按依赖图调度 todo：

- 依赖完成的 todo 才会进入 runnable 集合。
- 独立分支可按 `MaxParallelTodos` 并发执行。
- 某个 todo 失败后，下游依赖 todo 会被标记为 `blocked`。
- 如果配置了 `TodoReplanner`，失败后可以尝试应用 `ResearchTodoPlanPatch`。

调度器只关心依赖和状态，不关心 todo 内部如何研究。具体执行由 `TodoExecutor` 负责，默认实现是 `Runner.executeTodo`。

### 4. 单个 Todo 的执行

单个 todo 的默认执行流程：

```text
ResearchTodo
  |
  v
RuleBasedTodoDispatcher
  |
  v
TodoResearchJob[]
  |
  v
AgentResearcher[] 并行执行
  |
  v
ResearcherResult[]
  |
  v
AgentSynthesizer
  |
  v
StepExecution
  |
  v
gap check
  |
  +-- 有 gap 且未到上限 --> 带 prior context 再跑一轮
  |
  v
TodoExecution
```

`RuleBasedTodoDispatcher` 根据 todo 内容派发 researcher 角色：

- 普通 todo：`background_researcher`、`evidence_researcher`、`counterpoint_researcher`。
- 时效性 todo：额外加入 `freshness_researcher`。
- synthesis todo：使用 `synthesis_researcher` 和 `gap_checker`。

角色数量受 `research.max_researchers_per_todo` 限制。

### 5. Researcher 并行研究

每个 `AgentResearcher` 是一个 Eino `ChatModelAgent`，绑定：

- role：稳定角色 ID。
- focus：该角色研究视角。
- tools：`web_search` 和 `web_fetch`。

researcher 的目标输出是 `ResearcherResult`：

```json
{
  "role": "evidence_researcher",
  "focus": "authoritative evidence...",
  "queries": ["实际使用或建议的 query"],
  "findings": [
    {
      "claim": "可被证据支撑的判断",
      "rationale": "为什么证据支持该判断",
      "source_ids": ["todo_1_src_1"],
      "evidence_refs": [
        {"source_id": "todo_1_src_1", "chunk_id": "todo_1_src_1_chunk_1", "quote": "证据摘录"}
      ]
    }
  ],
  "sources": [],
  "documents": [],
  "errors": []
}
```

当前容错策略：

- 单个 researcher 失败不会导致 todo 失败。
- 只有所有 researcher 都失败时，`ParallelStepExecutor` 才会返回错误。
- researcher 如果返回非 JSON 文本，会被包装成一个 finding，交给 synthesis 继续处理。

### 6. 工具和证据采集

当前暴露给 researcher 的工具：

`web_search`

- 入参：`query`、`limit`。
- 调用 `search.Provider`。
- 按 todo/step 前缀重写 source ID，避免不同 todo 的 `src_1` 冲突。
- 受 `search.max_searches_per_step` 和 `search.results_per_search` 控制。

`web_fetch`

- 入参：`url`、`max_chars`。
- 读取 HTTP/HTTPS 页面。
- 抽取 HTML 可见文本。
- 成功抓取的页面会写入 `FetchedPageStore`。

证据数据会进入两类结构：

- `search.Source`：来源级元数据，例如 title、url、snippet、provider、query。
- `SourceDocument` / `SourceChunk`：可引用文本切片，用于 claim-level evidence。

如果没有 fetch 正文，系统会用搜索结果 snippet/title 构造兜底 document。如果有 fetch 正文，fetch document 会优先于 snippet document。

### 7. Synthesis 和 Gap Retry

所有 researcher 完成后，`AgentSynthesizer` 把多个 `ResearcherResult` 合成为 `StepExecution`：

```json
{
  "step": {},
  "researcher_results": [],
  "summary": "本 todo 的综合结论",
  "gaps": ["仍缺失的问题"],
  "sources": [],
  "documents": []
}
```

如果 synthesizer 返回非 JSON，系统会保留原文作为 summary，并继续携带 researcher 结果和 sources，避免已经采集到的证据丢失。

每轮 synthesis 后会执行 deterministic gap check：

- summary 是否为空。
- 是否有 researcher result。
- 是否有 findings 或 sources。
- evidence-oriented todo 是否缺少 sources。
- synthesizer 是否主动报告 gaps。

如果存在 gap，且没有达到 `research.max_todo_research_iterations`，系统会把“依赖 todo 结果 + 前几轮尝试结果”作为 prior context，再执行下一轮研究。

### 8. 证据归一化

`normalizeStepExecutionSources` 是 step 结果进入上层前的统一证据处理入口，主要做：

- 合并多个 researcher 的 sources。
- 按 URL 去重并重写 source ID。
- 同步重写 findings/evidence_refs 中的 source_id。
- 从 source snippet/title 或 fetched page 构造 `SourceDocument`。
- 把 document 切成 `SourceChunk`。
- 为缺失的 evidence_refs 自动补齐 chunk/quote。

最终 `TodoExecution.Findings` 会尽量具备：

- `claim`
- `rationale`
- `source_ids`
- `evidence_refs`

这让 Markdown 报告和 JSON 输出都可以追踪到具体证据片段。

### 9. Result 聚合和渲染

所有 todo 执行完后，`Runner.Execute` 聚合为 `ResearchResult`：

```json
{
  "question": "用户原始问题",
  "answer": {
    "markdown": "最终 Markdown 正文",
    "summary": "简短摘要",
    "key_findings": [],
    "limitations": []
  },
  "plan": {},
  "section_executions": [],
  "todo_executions": [],
  "sources": [],
  "documents": [],
  "metadata": {},
  "error": null
}
```

聚合规则：

- `TodoExecutions` 保留实际调度返回顺序。
- `SectionExecutions` 按原始 plan.sections 和 plan.todos 顺序重排，保证报告结构稳定。
- `Sources` 从所有 todo sources 汇总并按 URL 去重。
- `Documents` 从所有 todo documents 汇总并去重。
- `Answer.Summary` 当前为完成 todo 数摘要。

Markdown renderer 会输出：

- 主回答正文。
- Execution Summary。
- Findings and Evidence。
- Sources。

JSON renderer 会保留完整结构，适合调试和后续系统集成。

## 状态和错误边界

todo 状态：

- `pending`：尚未调度。
- `running`：预留给未来事件流。
- `done`：成功完成。
- `failed`：executor 返回错误。
- `blocked`：依赖失败或无法继续推进。
- `skipped`：replanner 显式跳过。

错误记录：

- `ResearchResult.Error.Stage` 标记失败阶段，例如 `input`、`plan`、`scheduler`、`execute`。
- CLI JSON 输出会包含结构化错误。
- Markdown 模式下执行失败会写 stderr。

## 设计取舍

当前实现刻意把几个职责分开：

- Planner 只拆 todo plan，不直接决定子代理角色。
- Scheduler 只做依赖调度，不理解 researcher 细节。
- Dispatcher 负责 todo 到 researcher jobs 的 fan-out。
- Executor 负责单个 todo 的并行研究、synthesis 和 bounded retry。
- Evidence 层负责 source/document/chunk/evidence_ref 的归一化。

这样后续可以独立优化：

- 用更强的结构化输出约束替换 researcher/synthesizer 的纯 JSON prompt。
- 为每轮 retry 建立独立工具预算。
- 加入最终全局 answer synthesis。
- 强化 gap checker 和 acceptance criteria 检查。
- 引入更丰富的搜索、抓取、解析和 rerank 工具。

## 开发验证

运行全部测试：

```bash
go test ./...
```

检查当前分支状态：

```bash
git status --short
```
