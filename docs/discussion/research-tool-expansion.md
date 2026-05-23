# Research 工具扩展记录

本文记录 deep research 能力扩展中的工具层方案和落地顺序。

## 当前问题

项目早期只有 `web_search` 工具，能拿到搜索结果的 title、url、snippet，但无法读取网页正文。这样会导致研究结果更依赖搜索摘要，难以做到 deep research 产品常见的“打开来源、阅读正文、再引用和综合”。

## 工具扩展顺序

优先级：

1. `web_fetch`：读取搜索结果 URL 的正文内容。
2. PDF fetch/parse：读取论文、报告、白皮书。
3. file reader：读取用户上传或本地文件。
4. domain/site scoped search：限定来源域名或优先来源。
5. source quality/ranking：对来源做可信度、重复、时效性判断。

## 本轮落地：web_fetch

已新增：

- `web_fetch` tool：输入 URL，输出 URL、title、可读正文 text、content type。
- `HTTPPageFetcher`：默认 HTTP/HTTPS 抓取实现。
- HTML 解析：使用 `golang.org/x/net/html`，过滤 `script`、`style`、`noscript`、`svg` 文本。
- 限制能力：支持每个 step 的 fetch 次数限制、最大正文字符数、最大响应体字节数。
- 多工具接入：researcher agent 现在同时可用 `web_search` 和 `web_fetch`。

当前策略：

- researcher 先用 `web_search` 找来源，再用 `web_fetch` 阅读关键 URL。
- `web_fetch` 只支持 HTTP/HTTPS，拒绝 `file://` 等非网络 URL。
- 正文默认截断，避免工具输出撑爆模型上下文。

## 后续注意

- PDF 和网页正文应统一为 source document 模型，方便后续 claim-level citations。
- 长网页需要 chunking，否则单次工具返回仍可能丢失关键段落。

## Todo 内多角色派发

问题：

Planner 负责把研究目标拆成 todo 和依赖，但单个 todo 内部仍可能需要不同视角的深度研究。让 planner 直接输出每个子代理配置会让 plan schema 变复杂，也会增加格式校验和 repair 的成本。

当前选型：

- 使用代码派发，而不是 manager agent 动态派发。
- 每个 runnable todo 先经过 `TodoDispatcher`。
- 第一版使用 `RuleBasedTodoDispatcher`，按 todo 类型确定性派生 researcher jobs。
- 每个 job 共享该 todo 的工具预算，避免子代理数量和搜索/fetch 次数失控。

已落地派发规则：

- 普通/evidence todo：`background_researcher`、`evidence_researcher`、`counterpoint_researcher`。
- 时效性 todo：优先保留 `freshness_researcher`，默认 cap 下会替换掉低优先级角色。
- synthesis/conclusion todo：`synthesis_researcher`、`gap_checker`。

执行链路：

```text
TodoScheduler
  -> executeTodo
  -> RuleBasedTodoDispatcher
  -> buildTodoResearchers
  -> ParallelStepExecutor
  -> AgentSynthesizer
  -> TodoExecution
```

当前行为：

- `executeTodo` 已从简单 search 汇总升级为 todo 内多 researcher fan-out。
- researcher agent 可使用 `web_search` 和 `web_fetch`。
- dependency todo execution 会转换为 prior executed steps，传给当前 todo 的 researcher。
- synthesis 输出会转换回 `TodoExecution`，保留 researcher results、findings、gaps、sources。

后续注意：

- 需要增加更细的预算控制，例如 `MaxParallelResearchJobs`。
- 后续可以把规则派发升级为“规则优先 + 可选模型派发”，但模型派发必须有 schema 校验和 fallback。

## Todo 内迭代深挖

第 4 点“迭代深挖”的选型：

主流实现差异：

- LLM 自主 tool loop：researcher 自己决定搜索、读取、改写 query、停止。灵活，但预算和终止条件依赖 prompt。
- 代码控制 bounded loop：代码决定最多执行几轮、何时停止、何时带着 gaps 继续。稳定、可测试、预算可控。
- Graph/state-machine loop：用显式状态图表达 search/read/critique/route/synthesize。可观测性强，但实现复杂度更高。

当前选择：

- 暂不采用完全自主的 LLM tool loop 作为外层控制，避免预算和终止条件不可控。
- 暂不引入模型 dispatcher/manager 来决定每轮派发。
- 采用代码控制的 bounded loop：每个 todo 最多执行 `max_todo_research_iterations` 轮。
- 每轮执行仍由子代理自行使用 `web_search` / `web_fetch`。
- 每轮 synthesis 后使用确定性 gap 判断决定是否继续。

第 5 点“子代理角色选择”的选型：

主流实现差异：

- 固定角色 fan-out：每个任务都派 background/evidence/counterpoint 等固定角色。稳定但可能有浪费。
- 规则路由角色：根据 todo 类型和关键词选择角色。仍然可控，并且比固定 fan-out 更节省。
- Supervisor/manager agent：由 manager 动态决定派给哪些 specialist。灵活，但 manager 也需要校验和兜底。
- Selector/group chat：多个 agent 共享上下文，由模型选择下一个 speaker。探索性强，但上下文膨胀和终止控制更难。
- Planner 输出 roles：让 plan schema 直接包含子代理角色。定制性好，但会显著增加 planner 格式复杂度。

当前选择：

- 保持 planner 只负责 todo 和依赖拆解。
- 派发层用 `RuleBasedTodoDispatcher` 做规则路由角色。
- 后续如果加入模型派发，也只作为规则派发之后的可选增强，并必须有 schema 校验和 fallback。

第一版 gap 判断：

- synthesis summary 为空。
- researcher results 为空。
- 没有 findings 且没有 sources。
- evidence-oriented todo 没有 sources。
- synthesizer 显式返回了 `gaps`。

当前行为：

- 如果一轮后没有 gap，立即停止。
- 如果仍有 gap 且未达到上限，上一轮 StepExecution 会作为 prior executed step 传入下一轮。
- 如果达到上限仍有 gap，返回最后一轮结果，并保留 gap 信息，避免把未解决问题静默吞掉。
- 配置层已暴露：
  - `research.max_researchers_per_todo`
  - `research.max_todo_research_iterations`
