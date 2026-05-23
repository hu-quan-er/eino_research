# Planner 输出可靠性方案对比

本文记录 research agent 在 `ResearchTodoPlan` 生成阶段的格式可靠性问题、可选方案和当前落地顺序。

## 背景

当前 auto-todo 主流程依赖 Planner 生成 `ResearchTodoPlan`。该结构会驱动后续 todo 调度、依赖判断、执行摘要和 JSON 输出。如果 Planner 输出不是合法 JSON、字段名错误、缺少必填字段、引用不存在的 section/todo，执行阶段应在确认前停止，或尽量自动修复。

单纯在执行前做 `Validate()` 可以阻止坏计划进入执行阶段，但它只负责拒收，不能减少模型输出不稳定带来的失败率。

## 方案 A：详细错误 + repair retry

做法：

- `parseResearchTodoPlan` 返回详细 error，而不是 bool。
- `Runner.Plan` 首次生成失败后，把原始输出和具体错误反馈给模型。
- repair prompt 要求模型只返回修复后的 `ResearchTodoPlan` JSON。
- 最多重试有限次数，仍失败则返回可诊断错误。

优点：

- 实现范围小。
- 对所有模型兼容。
- 可显著提升非严格 JSON 输出和轻微 validation 错误的成功率。
- 错误信息对 CLI 和调试更有价值。

缺点：

- 仍依赖模型自我修复，不是强约束。
- 多一次模型调用会增加耗时和成本。

## 方案 B：tool/function calling planner

做法：

- 定义 `create_research_todo_plan` tool，参数 schema 对应 `ResearchTodoPlan`。
- Planner 优先要求模型通过 tool call 返回结构化参数。
- 从 tool call arguments 解析 plan，再做业务 `Validate()`。

优点：

- 比普通文本 JSON 更稳定。
- 与当前 `model.ToolCallingChatModel` 类型契约匹配。
- 能显式约束字段结构，减少 Markdown 包裹、自然语言前后缀等问题。

缺点：

- 依赖模型和 provider 对 tool calling 的支持质量。
- 业务约束仍需要本地 validator，例如依赖图无环、引用存在。
- 需要 fallback，避免某些 OpenAI-compatible provider tool call 行为不一致。

## 方案 C：provider-native structured output

做法：

- 使用 provider 原生 JSON schema / structured output / response schema。
- 开启 strict schema 时，让 provider 在输出层约束结构。

优点：

- 理论上最强的结构化输出能力。
- 对支持严格 schema 的 provider，失败率最低。

缺点：

- Eino 当前抽象和 OpenAI-compatible provider 可能无法统一暴露该能力。
- 不同 provider 支持的 JSON Schema 子集不一致。
- 仍然需要本地业务 validator。

## 当前选型

按以下顺序落地：

1. 先做 **详细错误 + repair retry**，立即提升可诊断性和恢复能力。
2. 再做 **tool-calling planner**，优先走工具参数结构化输出。
3. 保留普通 JSON + repair retry fallback，兼容不稳定的 OpenAI-compatible provider。
4. 后续如果 Eino 或 provider adapter 明确支持统一的 strict structured output，再升级为 provider-native structured output。

这条路线的原则是：先提高可靠性和可观测性，再逐步增强生成时约束，同时避免和具体 provider 深绑定。

## 本轮落地记录

已落地：

- `parseResearchTodoPlan` 从 `bool` 改为返回详细 `error`，区分 JSON 解析错误和业务校验错误。
- `ResearchTodoPlan.Validate()` 增加空 `sections`、空 `todos` 的显式拒收，避免空计划进入执行阶段。
- `Runner.Plan` 增加普通 JSON planner 的 repair retry：最多 3 次，失败时把上一轮输出和具体错误回灌给模型。
- 增加 `create_research_todo_plan` tool schema，并让 `Runner.Plan` 优先走 tool calling planner。
- tool calling 输出仍会经过本地 `ResearchTodoPlan` 解析和业务校验。
- tool binding 不支持、tool call 缺失、tool arguments 不合规时，回退到普通 JSON + repair retry 路径。

验证覆盖：

- 非 JSON planner 输出会触发修复重试。
- 连续修复失败会返回包含最后一次校验原因的错误。
- tool calling planner 成功时只需一次模型调用。
- tool calling 返回不合规时会回退到文本修复路径。

## PlanLinter 语义质量门禁

问题：

`Validate()` 只能保证结构合法，例如字段存在、依赖引用存在、依赖图无环。但 planner 仍可能输出“结构合法、执行质量差”的计划，例如 evidence todo 没有搜索 query、多个 todo 问同一个问题、synthesis todo 没依赖任何 evidence todo。

选型：

- 暂不引入模型 Judge，避免新增不稳定输出、额外成本和延迟。
- 先使用规则型 `PlanLinter` 做确定性检查。
- error 级 issue 直接进入已有 repair retry；warning 级 issue 先只保留能力，不阻断计划。

已落地规则：

- `section_without_todos`：section 下没有任何 todo。
- `duplicate_todo_question`：多个 todo 使用相同问题。
- `todo_question_too_generic`：todo question 是明显泛化占位。
- `acceptance_criteria_too_generic`：验收标准是明显泛化占位。
- `missing_search_queries`：证据收集类 todo 没有搜索 query。
- `search_query_too_long`：搜索 query 过长，不适合直接检索。
- `duplicate_search_query`：搜索 query 重复。
- `synthesis_missing_dependencies`：综合/结论类 todo 没有依赖前置 todo。

当前行为：

- tool calling 和普通 JSON planner 都会经过同一套 `Validate()` + `PlanLinter`。
- tool call arguments 如果触发 lint error，会作为上一轮 planner 输出传入 repair prompt。
- repair 后仍会重新经过结构校验和语义 lint。
