# Architecture Evolution: EventBus, SectionSynthesizer, ReflectionPass

日期：2026-05-28

## 背景

当前 deep research 主流程已经覆盖 Planner、TodoScheduler、RuleBasedTodoDispatcher、AgentResearcher、AgentSynthesizer、bounded gap retry、FinalSynthesizer、EvidenceBinder、ClaimVerifier 七大模块，所有"高级"环节都有规则版 v1。`docs/discussion/deep-research-effect-roadmap.md` 已记录广度上的下一轮增强清单（eval CLI、段落级 claim 抽取、reranker、模型 CitationAgent 等）。

但当前架构在三个维度仍是薄弱点：

- **缺反思闭环**：unsupported claim、open gap、failed todo 只会被写入 `Answer.Limitations`，没有任何机制把它们转回 plan 层做定向补搜。`TodoReplanner` 接口存在但无默认实现，`gap_checker` 只是一个 researcher 角色，不会改变 plan。
- **缺可观测性**：CLI 是阻塞的，plan/dispatch/researcher/synthesis 全部黑盒，失败时只能看最终 `Error.Stage` 字符串；没有 token/调用/耗时的结构化记录，无法支撑后续 HTTP 服务、eval 复盘、reflection 输入。
- **中间层不均衡**：Todo→Section→Final 三层中 Section 实际上不存在——`SectionExecution.Summary` 只是把同一 section 下 todo summary 简单拼接，`FinalSynthesizer` 一次性吃进全部 todo 结果，承担"分段归纳 + 跨段综合"两件事，prompt 长、容易降质、失败面大。

本设计提出按三个阶段演进架构：先建 EventBus 作为基础设施，再补 SectionSynthesizer 作为中间归纳层，最后引入 ReflectionPass 闭环。三个阶段独立交付、独立验证，但共享一份统一的最终架构。

## 决策摘要

- 采用 **B 方案：三层分阶段增量**。本 spec 给出统一最终架构 + Phase 1 详细设计；Phase 2/3 仅给接口轮廓和动机，落地前各自走单独的 brainstorm→spec→plan 循环。
- 三个阶段顺序固定为 **EventBus → SectionSynthesizer → ReflectionPass**。EventBus 是另外两者的基础设施；Section 让 Final 输入更紧凑，也给 Reflection 提供合适粒度的反思单元。
- 不引入新的 plan schema、不重写 `ResearchTodoPlan`、不破坏 `Runner.Plan` / `Runner.Execute` 现有签名。所有阶段以"可选注入 + nil 降级"的方式接入。

## 最终目标架构

```
Runner.Run / Plan / Execute
  │
  ├── EventBus  ◄── Phase 1（新增基础设施，所有阶段都向它发事件）
  │     │
  │     ├── EventSink: JSONLinesSink（CLI --stream，HTTP/SSE 复用）
  │     ├── EventSink: TraceStore（运行结束 dump 到 result.metadata.trace[]）
  │     └── EventSink: BudgetMeter（聚合 token/调用次数到 result.metadata.budget）
  │
  ▼
  Plan ───► PlanEvent
  │
  ▼
  TodoScheduler ───► TodoStartEvent / TodoDoneEvent / TodoFailedEvent
  │
  ▼
  executeTodo (Dispatcher + Researchers + AgentSynthesizer + gap-retry)
  │           ───► ResearcherEvent / ToolCallEvent / SynthesisEvent
  │
  ▼
  SectionSynthesizer  ◄── Phase 2（新增中间层）
  │  按 section 把 todo 结果先聚合，输出 SectionAnswer
  │  ───► SectionSynthesisEvent
  │
  ▼
  FinalSynthesizer
  │  输入从"全部 TodoExecution"换成"SectionAnswer[]"，prompt 更短、更稳
  │  ───► FinalSynthesisEvent
  │
  ▼
  EvidenceBinder ─► VerifyClaimsEvent
  ClaimVerifier  ─► UnsupportedClaimsEvent
  │
  ▼
  ReflectionPass  ◄── Phase 3（新增反思闭环）
  │  消费 trace + unsupported claims + gaps
  │  产出 ResearchTodoPlanPatch（targeted follow-up todos）
  │  通过现有 TodoReplanner 接口回灌到 scheduler 继续一轮
  │  ───► ReflectionEvent / PatchEvent
  │
  ▼
  ResearchResult（多了 Sections[] + Trace + Reflection 痕迹）
```

**关键不变量**：

- EventBus 之外，所有阶段保持纯函数式（输入→输出），事件只用于副作用观测，不影响主路径正确性。
- Phase 2 引入 SectionSynthesizer 时，FinalSynthesizer 接口不破坏（仍接受 SectionExecutions/TodoExecutions），但增加一条 Sections 字段，老路径作为 fallback。
- Phase 3 复用现有 `TodoReplanner` 接口和 `ResearchTodoPlanPatch`，只增加一个默认实现 + 在 `Runner.Execute` 末尾增加一个有限轮 reflection loop（受新的 `MaxReflectionPasses` 预算约束，默认 1）。

## Phase 1 详细设计：EventBus + Trace + BudgetMeter

### 事件模型

新增 `internal/research/events.go`：

```go
type EventKind string

const (
    EventPlanStarted     EventKind = "plan.started"
    EventPlanCompleted   EventKind = "plan.completed"
    EventTodoStarted     EventKind = "todo.started"
    EventTodoCompleted   EventKind = "todo.completed"
    EventTodoFailed      EventKind = "todo.failed"
    EventDispatch        EventKind = "todo.dispatched"
    EventResearcherStart EventKind = "researcher.started"
    EventResearcherDone  EventKind = "researcher.completed"
    EventToolCall        EventKind = "tool.call"      // web_search / web_fetch
    EventSynthesis       EventKind = "synthesis.completed"
    EventGapRetry        EventKind = "todo.retry"
    EventFinalStart      EventKind = "final.started"
    EventFinalCompleted  EventKind = "final.completed"
    EventEvidenceBound   EventKind = "evidence.bound"
    EventClaimsVerified  EventKind = "claims.verified"
)

type Event struct {
    Kind        EventKind       `json:"kind"`
    At          time.Time       `json:"at"`
    RunID       string          `json:"run_id"`
    TodoID      string          `json:"todo_id,omitempty"`
    Role        string          `json:"role,omitempty"`
    Attempt     int             `json:"attempt,omitempty"`
    DurationMS  int64           `json:"duration_ms,omitempty"`
    TokensIn    int             `json:"tokens_in,omitempty"`
    TokensOut   int             `json:"tokens_out,omitempty"`
    Tool        string          `json:"tool,omitempty"`
    Query       string          `json:"query,omitempty"`
    URL         string          `json:"url,omitempty"`
    Message     string          `json:"message,omitempty"`
    Err         string          `json:"error,omitempty"`
    Payload     json.RawMessage `json:"payload,omitempty"` // 阶段相关结构化负载
}

type EventSink interface {
    Emit(ctx context.Context, e Event)
}

type EventBus struct { sinks []EventSink }
func (b *EventBus) Emit(ctx context.Context, e Event)
func (b *EventBus) Add(sink EventSink)
```

**注入方式**：`RunnerConfig` 新增 `Events *EventBus`。`Events == nil` 时 Runner 内部使用一个 no-op bus，所有 `Emit` 调用静默返回，保证现有调用方零改动。

**Run ID**：`Runner.Plan` 和 `Runner.Execute` 分别在入口生成或继承一个 run ID，作为事件 RunID 字段；library 调用方可通过 ctx value 注入外部 run ID（HTTP 服务复用）。

### 三个内置 Sink

新增 `internal/research/event_sinks.go`：

1. **`JSONLinesSink(w io.Writer)`**
   - 每条事件一行 JSON，写入提供的 `io.Writer`（CLI `--stream` 时挂到 stderr）。
   - 内部使用 `sync.Mutex` 防止并发 todo 的事件 interleave 撕裂行。
   - 写入失败仅记录到 stderr，不返回错误（best-effort）。

2. **`TraceStore`**
   - 内存累积所有事件，提供 `Snapshot() []Event`。
   - `Runner.Execute` 结束时调用 `Snapshot()` 注入 `ResearchResult.Metadata.Trace`（新增字段，类型 `[]Event`，omitempty）。
   - 设置上限 `MaxEvents`（默认 5000），超过后丢弃最早事件并 emit 一条 warning 事件。

3. **`BudgetMeter`**
   - 聚合统计，提供 `Snapshot() BudgetReport`：
     ```go
     type BudgetReport struct {
         TodosCompleted    int            `json:"todos_completed"`
         TodosFailed       int            `json:"todos_failed"`
         ToolCalls         map[string]int `json:"tool_calls"`    // web_search / web_fetch
         ModelCalls        int            `json:"model_calls"`
         TokensIn          int            `json:"tokens_in"`
         TokensOut         int            `json:"tokens_out"`
         DurationMS        int64          `json:"duration_ms"`
         ReflectionPasses  int            `json:"reflection_passes"` // Phase 3 用，Phase 1 永远为 0
     }
     ```
   - 注入 `ResearchResult.Metadata.Budget`（新增字段）。

### 改造点（Phase 1 落地代码改动面）

| 文件 | 改动 |
|---|---|
| `internal/research/events.go` | 新增 EventKind、Event、EventSink、EventBus |
| `internal/research/event_sinks.go` | 新增 JSONLinesSink、TraceStore、BudgetMeter |
| `internal/research/runner.go` | `RunnerConfig.Events`；`Plan` / `Execute` 入口生成 RunID；阶段边界 emit；nil-bus fallback |
| `internal/research/runner.go` (`executeTodo`) | dispatcher 之后、每个 researcher 之前/之后、synthesizer、gap retry 各 emit 一条 |
| `internal/research/tools.go` | `web_search` / `web_fetch` 包装层增加 emit `EventToolCall` 钩子（构造工具时传入可选 emit 回调） |
| `internal/research/result.go` | `Metadata` 新增 `Trace []Event` 和 `Budget *BudgetReport`（指针，允许 omitempty） |
| `cmd/research/main.go` | 新增 `--stream`（JSONL 到 stderr）和 `--trace`（JSON 输出包含 trace） |
| `cmd/research/main_test.go` | 覆盖两个新 flag 的开/关行为 |

### 验证策略

- **单元测试**：
  - 每个 EventKind 至少一个 emit→fake sink 路径验证。
  - JSON 序列化往返稳定。
  - JSONLinesSink 并发 100 goroutines emit，断言输出行数正确、不撕裂。
  - TraceStore 超 MaxEvents 时丢弃最早、emit warning。
  - BudgetMeter 对若干模拟事件做累加，断言 ToolCalls / TokensIn/Out 正确。
- **集成测试**：
  - 用 mock provider 跑一次 `Runner.Run`，断言事件拓扑包含 `plan.started → plan.completed → ≥1 todo.started → ≥1 todo.completed → final.started → final.completed`，且没有未配对的 started/completed。
  - 现有所有测试不变（验证非破坏性）。
- **CLI 烟测**：`go run ./cmd/research --provider mock --yes --stream "<question>"`，肉眼可读事件流；`--trace --json` 时 JSON 输出包含完整 trace。
- **失败注入**：sink panic 必须被 recover，不影响主路径返回值。

### 显式不做（Phase 1 范围控制）

- 不做 OpenTelemetry / OTLP 导出。先 JSONL；需要时再加 OTel sink，接口已为此预留。
- 不做事件回放或缓存。replay 是 eval/cache 的话题，留给独立 spec。
- 不修改 `ResearchResult` 已有字段语义，仅追加 `Metadata.Trace` 和 `Metadata.Budget` 两个可选字段。
- 不实现 token 计数自动埋点。`TokensIn/TokensOut` 字段先留空，待模型层暴露用量时再补；BudgetMeter 字段先到 0。
- 不在 Markdown 渲染器里输出 trace。trace 只进 JSON 输出。

## Phase 2 接口轮廓：SectionSynthesizer

仅给接口和动机，详细设计留待 Phase 1 落地后单独 spec。

```go
type SectionSynthesizer interface {
    SynthesizeSection(ctx context.Context, in SectionSynthesisInput) (SectionAnswer, error)
}
type SectionSynthesisInput struct {
    Question  string
    Section   ResearchSection
    Todos     []TodoExecution
    Documents []SourceDocument
}
type SectionAnswer struct {
    SectionID    string
    Title        string
    Summary      string
    KeyFindings  []string
    Limitations  []string
    Citations    []ClaimEvidence
}
```

**动机**：

- 当前 `FinalSynthesizer` 把所有 todo 结果整包喂模型，prompt 长、模型必须同时做"分段归纳 + 跨段综合"两件事，质量和稳定性受限。
- 引入 section 层后，Final 输入从 N 个 todo 压缩到 M 个 section answer，token 显著降低。
- 每个 section 可独立失败重试，不影响其他 section。
- Phase 3 的 reflection 可以在 section 粒度判断"这一节证据够不够"，比 todo 粒度更贴近用户视角。

**与现有结构关系**：复用现有 `SectionExecution` 字段名空间，把 `SectionExecution.Summary` 升级为来自 `SectionAnswer` 的结构化产物；旧调用方仍能读到 Summary 字段。

## Phase 3 接口轮廓：ReflectionPass

```go
type ReflectionPass interface {
    Reflect(ctx context.Context, in ReflectionInput) (ReflectionOutput, error)
}
type ReflectionInput struct {
    Question         string
    Plan             ResearchTodoPlan
    Sections         []SectionAnswer
    Answer           Answer
    UnsupportedClaims []ClaimEvidence
    OpenGaps         []string
    Trace            []Event   // 来自 Phase 1 的 TraceStore
}
type ReflectionOutput struct {
    Patch          ResearchTodoPlanPatch  // 复用现有 patch 类型
    StopReason     string                 // "no_gap" / "budget_exhausted" / "no_actionable_followups"
}
```

**Runner 改动**：`Execute` 末尾增加 bounded reflection loop（受 `RunnerConfig.MaxReflectionPasses` 约束，默认 1）：

```
for pass := 0; pass < MaxReflectionPasses; pass++ {
    refl, err := reflector.Reflect(ctx, ...)
    if err != nil || refl.Patch.IsEmpty() { break }
    follow := scheduler.RunPatch(ctx, plan, refl.Patch)
    // 把 follow-up TodoExecutions 合入 result，重跑 SectionSynth + Final + Bind + Verify
}
```

**动机**：当前 unsupported claim 只会写进 limitations，等于"知道哪里不行但什么也不做"。Reflection 让"verifier 发现的 gap"直接驱动"定向补搜"，把 ClaimVerifier 从门禁升级为诊断 + 处方。

**默认实现**：先做 `RuleBasedReflector`（不调用模型）——对每条 unsupported claim 生成一个 follow-up todo，复用 todo dispatcher / researcher 流程。后续可选 `AgentReflector` 由模型生成 patch，但必须有 schema 校验和 budget cap。

## 阶段交付顺序与验证

| 阶段 | 交付物 | 关键验证 |
|---|---|---|
| Phase 1 | EventBus + 3 个 sink + CLI `--stream` / `--trace` + `Metadata.Trace` / `Metadata.Budget` | mock 跑一次 Run，事件拓扑断言通过；现有所有测试不变；CLI `--stream` 人工肉眼可读；sink panic 不影响主路径 |
| Phase 2 | SectionSynthesizer 接口 + AgentSectionSynthesizer + Runner 集成 | `FinalSynthesisInput` 增加 SectionAnswers 字段；section 失败时降级为旧路径；eval harness 上对同一问题 before/after 对比 |
| Phase 3 | ReflectionPass 接口 + RuleBasedReflector + AgentReflector + Runner reflection loop | 制造一个 unsupported claim case，验证 reflection 生成 patch + 二次执行后 claim 被支撑；预算耗尽时干净停止 |

每个阶段完成后再走单独的 brainstorm→spec→plan 循环；本 spec 只对 Phase 1 形成可立即实施的 plan。

## 风险与权衡

- **EventBus 引入并发复杂度**：todo 并行执行时多 goroutine 同时 emit。通过 sink 内部 mutex 保证顺序写入；事件本身按时间戳排序而非到达顺序。
- **Trace 体积膨胀**：长 run 可能生成大量事件。MaxEvents 上限 + emit warning 保护；JSON 输出层可选择性渲染（默认开启）。
- **Token 计数缺位**：Phase 1 不强制接 token 用量，因为 Eino 当前层未稳定暴露。先把字段留好，后续 model 层补全时无破坏性。
- **Phase 2/3 接口可能变化**：本 spec 给出的轮廓是方向性的，实际 spec 阶段可能调整字段。Phase 1 不依赖任何 Phase 2/3 类型，因此变化不会回灌。

## 不在本 spec 范围

- eval harness CLI 与结果持久化（独立 spec）。
- search/fetch 缓存与 record/replay（独立 spec）。
- 多搜索 provider 与 rank fusion（独立 spec）。
- 段落级 claim 抽取与模型 CitationAgent（独立 spec）。
- 模型分级（plan 用强模型、researcher 用便宜模型）（独立 spec）。
