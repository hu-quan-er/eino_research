# Phase 2 — SectionSynthesizer Design

日期：2026-05-31

> 本文是架构演进 Phase 2 的详细设计，落地 `docs/superpowers/specs/2026-05-28-architecture-evolution-design.md` 中给出的 SectionSynthesizer 接口轮廓。Phase 1（EventBus）已完成并接入主路径，本设计复用其事件能力。

## 背景与动机

当前 `Runner.Execute` 在 todo 调度完成后，用 `groupTodoExecutionsBySection` 把 todo 结果按 plan section 分组为 `[]SectionExecution`，其 `Summary` 只是把同 section 下各 todo 的 summary 简单字符串拼接；随后 `FinalSynthesizer` 一次性吃进**全部** todo（`buildFinalSynthesisContext` 会把每个 todo 的 findings、researcher 快照都塞进 prompt）。

这带来三个问题：

- **Final prompt 过长**：todo 越多，prompt 越大，模型要同时做"分段归纳 + 跨段综合"两件事，质量与稳定性受限。
- **无失败隔离**：没有 section 粒度的中间归纳单元，任何一段证据不足都只能在最终一锅端里体现。
- **缺反思粒度**：Phase 3 的 reflection 希望以"这一节证据够不够"为单元判断，比 todo 粒度更贴近用户视角，但当前没有 section 级结构化产物。

本设计在 Todo→Final 之间引入 **SectionSynthesizer** 中间归纳层：对每个 section 先产出结构化 `SectionAnswer`，使 Final 输入从"全部 todo"压缩为"M 个 section answer"，并为每个 section 提供独立失败隔离与结构化产物。

## 决策摘要

- **方案 A**：独立 `SectionSynthesizer` 组件，与现有 `FinalSynthesizer` / `EvidenceBinder` / `ClaimVerifier` 完全同构（nil 自动装配、可注入、可单测）。
- **SectionAnswer 精简版**：只含 `summary` + `key_findings`（内联 source id）+ `limitations`。**不做 section 级 claim→证据绑定**，证据绑定仍由下游现有 `EvidenceBinder` 统一负责，避免与下游重复。
- **默认开启**：`RunnerConfig.SectionSynthesizer` 为 nil 时 Runner 自动装配 `AgentSectionSynthesizer`，与其他组件一致。
- **v1 串行**合成各 section；并行留作后续增强。
- **确定性优先**：单 section 失败/非 JSON 时回退到确定性兜底，其他 section 不受影响；全部失败时 Final 回退到旧 todo-dump 路径，零回归风险。

## 不破坏的不变量

- `Runner.Plan` / `Runner.Execute` / `Runner.Run` 签名不变。
- `FinalSynthesizer` 接口不变（仍接受 `SectionExecutions` / `TodoExecutions`），只在 `FinalSynthesisInput` **追加** `SectionAnswers` 字段，老路径作为 fallback。
- `SectionExecution` 仅**追加**字段（`KeyFindings` / `Limitations`），`Summary` 语义保留（向后兼容 renderer），其值升级为来自 `SectionAnswer.Summary`。
- 现有所有测试保持通过（非破坏性）。

## 组件设计

### 1. 类型与接口

新增 `internal/research/section_synthesizer.go`：

```go
// SectionSynthesizer 把一个 section 下的 todo 结果归纳为结构化 SectionAnswer。
type SectionSynthesizer interface {
    SynthesizeSection(ctx context.Context, in SectionSynthesisInput) (SectionAnswer, error)
}

// SectionSynthesisInput 是单个 section 归纳的输入。
type SectionSynthesisInput struct {
    Question  string           // 用户原始问题
    Objective string           // plan.Objective
    Section   ResearchSection  // 当前 section 定义
    Todos     []TodoExecution  // 该 section 下的 todo 执行结果
    Documents []SourceDocument // 该 section 下各 todo 的 Documents 去重合并，用于内联 source id 提示
}

// SectionAnswer 是单个 section 的结构化归纳产物。
type SectionAnswer struct {
    SectionID   string   `json:"section_id"`
    Title       string   `json:"title"`
    Summary     string   `json:"summary"`
    KeyFindings []string `json:"key_findings"` // 内联标注 source id，如 "X 支持 Y [todo_1_src_1]"
    Limitations []string `json:"limitations"`
}
```

默认实现 `AgentSectionSynthesizer{model}`：

- prompt 要求：只用本 section 的 todo 结果与文档、与用户问题同语言、关键事实内联 source id（如 `[todo_1_src_1]`）、把弱支撑或无证据的结论移入 limitations。
- 返回单个 `SectionAnswer` JSON。
- 输出非 JSON、空 answer、或模型出错 → 返回错误，由上层 `synthesizeSections` 走确定性兜底（见第 4 节）。

### 2. SectionExecution 增强

`internal/research/todo_execution.go`：

```go
type SectionExecution struct {
    Section     ResearchSection    `json:"section"`
    Todos       []TodoExecution    `json:"todos"`
    Summary     string             `json:"summary"`                // 升级为 SectionAnswer.Summary，兜底时仍是拼接
    KeyFindings []string           `json:"key_findings,omitempty"` // 新增
    Limitations []string           `json:"limitations,omitempty"`  // 新增
}
```

### 3. RunnerConfig 与装配

`RunnerConfig` 新增：

```go
// SectionSynthesizer 可替换 section 级归纳器；未传入时使用 AgentSectionSynthesizer。
SectionSynthesizer SectionSynthesizer
```

`Runner.Execute` 中，`synthesizeSections` 在 `SectionSynthesizer == nil` 时使用 `NewAgentSectionSynthesizer(r.cfg.Model)`（默认开启）。

### 4. 数据流（Runner.Execute）

```
scheduler.Run → todoExecutions
  → groupTodoExecutionsBySection → []SectionExecution（Summary 拼接，作为兜底基线）
  → synthesizeSections(ctx, question, plan, sectionExecutions, documents)  ◄── 新增
        for each SectionExecution（v1 串行）:
          emit section.started
          ans, err := synthesizer.SynthesizeSection(...)
          if err == nil && ans 非空:
              用 ans 覆盖 SectionExecution.Summary/KeyFindings/Limitations
              收集 ans 进 []SectionAnswer
          else:
              确定性兜底：保留拼接 Summary，聚合该 section 下 todo 的 top findings → KeyFindings、
                          gaps/error → Limitations；该兜底 answer 也进 []SectionAnswer，
                          保证 Final 始终看到所有 section 的归纳
          emit section.completed
  → result.SectionExecutions = 增强后的 sections
  → FinalSynthesisInput{..., SectionAnswers: []SectionAnswer}
  → synthesizeFinalAnswer（Final 上下文改为读 section answers）
  → bindFinalEvidence → verifyFinalClaims（不变）
```

说明：兜底产生的 `SectionAnswer` 也会进入 `[]SectionAnswer`，因此只要有任意 section，`SectionAnswers` 就非空，Final 走紧凑路径；只有在没有任何 section（理论上 plan 校验已排除空 sections）时才回退 todo-dump。

### 5. Final 消费方式

`internal/research/final_synthesizer.go`：

- `FinalSynthesisInput` 新增 `SectionAnswers []SectionAnswer`。
- `buildFinalSynthesisContext`：
  - 当 `SectionAnswers` 非空：上下文 = `{question, objective, section_answers[], sources, documents}`，**不再** dump 全部 todo findings / researcher 快照。
  - 当 `SectionAnswers` 为空：保留当前 todo-dump 上下文（fallback）。
- `TodoExecutions` 仍保留在 `FinalSynthesisInput` 中，仅供 `fallbackFinalAnswer` 的确定性兜底使用。
- Final system prompt 微调：基于 section answers + documents 做跨段综合、解决冲突、内联 source id；其余（输出 Answer JSON、同语言、移动弱支撑 claim 到 limitations）不变。

### 6. 事件

复用 Phase 1 EventBus，新增两个 EventKind：

```go
EventSectionStarted   EventKind = "section.started"
EventSectionCompleted EventKind = "section.completed"
```

- `synthesizeSections` 在每个 section 归纳前后 emit，section id 通过现有 `Event.TodoID` 字段承载（避免 Event 结构再膨胀；语义上 section id 与 todo id 同属"阶段对象 id"），`Event.Message` 可选记录是否走了兜底。
- `BudgetMeter` 把 `section.completed` 计入 `ModelCalls`（与 `synthesis.completed` / `final.completed` / `plan.completed` 同类处理）。

## 验证策略（TDD）

- **AgentSectionSynthesizer 单测**：合法 JSON → SectionAnswer；非 JSON / 空 answer → 返回错误。
- **synthesizeSections 单测**：
  - 单 section 模型失败 → 该 section 走兜底（Summary 保留、KeyFindings/Limitations 来自聚合），其他 section 保留模型 answer。
  - 注入式 SectionSynthesizer 全部成功 → SectionExecution 带 KeyFindings/Limitations，`[]SectionAnswer` 长度等于 section 数。
- **buildFinalSynthesisContext 单测**：有 / 无 SectionAnswers 两条分支上下文形状正确（有时不含 todo dump，无时含）。
- **Runner 集成**：默认开启时 `result.SectionExecutions` 带结构化字段，`Metadata.Trace` 含 `section.started` / `section.completed`，`Metadata.Budget.ModelCalls` 含 section 调用。
- **非破坏性**：现有所有测试不变；`go test ./...` 与 `go test -race ./...` 全绿。
- **CLI 烟测**：`--stream` 下可见 `section.started` / `section.completed` 事件。

## 显式不做（范围控制 / YAGNI）

- section 级 claim → 证据绑定（交给现有 `EvidenceBinder`）。
- section 并行合成（v1 串行；并行留后续，受预算约束）。
- Markdown renderer 大改（`KeyFindings` / `Limitations` 先进 JSON 输出；Markdown 渲染增强单独处理）。
- Phase 3 ReflectionPass 接入（本设计只保证产出 section 粒度结构，供后续反思消费）。
- 模型分级（plan/section/final 用不同模型）——独立议题。

## 风险与权衡

- **成本增加**：默认开启意味着每 run 多 3–6 次模型调用。换来的是更短更稳的 Final prompt 与失败隔离；BudgetMeter 已能度量该成本，后续可据此做模型分级。
- **串行延迟**：v1 串行合成 section 增加端到端延迟；section 数量受 plan lint 限制在 3–6，可接受，必要时再上并行。
- **双重 summary 来源**：`SectionExecution.Summary` 既可能是拼接兜底也可能是模型产物——通过"始终用 SectionAnswer 覆盖（兜底也产出 SectionAnswer）"消除歧义，保证 Summary 永远来自一次明确的归纳。
