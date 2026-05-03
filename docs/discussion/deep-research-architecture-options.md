# Deep Research 架构落地路径对比

日期：2026-05-03

本文记录在已选择“方案三：Deep Research / Plan-Execute Agent”之后，基于 Eino v0.8.13 可以采用的几种落地路径。

## 背景

用户希望借这个项目熟悉 Eino 框架的实际使用，因此第一版不采用最简单的单轮搜索，也不只做 ReAct 多轮搜索，而是直接覆盖更复杂的研究流程。

本地已缓存的 Eino 版本是 `github.com/cloudwego/eino@v0.8.13`。该版本中与复杂 agent 相关的能力包括：

- `adk.NewChatModelAgent`：基于模型和工具的基础 agent。
- `adk.NewSequentialAgent`：顺序执行多个 agent。
- `adk.NewLoopAgent`：循环执行 agent，可用于 execute/replan。
- `adk.NewParallelAgent`：并行执行多个 agent。
- `adk/prebuilt/planexecute`：内置 Planner、Executor、Replanner 和 plan-execute-replan 组合。
- `adk/prebuilt/deep`：内置 Deep agent，支持任务工具、todo 工具、子 agent 和文件/命令类 middleware。

## 路径一：基于 Eino `prebuilt/planexecute`

### 形态

使用 Eino 内置的 plan-execute-replan 模式：

1. Planner 生成结构化研究计划。
2. Executor 执行当前第一步，执行时可以调用 `web_search` 工具。
3. Replanner 根据已执行步骤判断是否完成，未完成则生成剩余计划。
4. Loop 持续执行 Executor + Replanner，直到 Replanner 调用 respond 或达到最大迭代次数。

### 学习价值

- 能清楚看到 Eino ADK 的 Planner、Executor、Replanner 如何组合。
- 能接触 tool calling、session values、loop agent、sequential agent。
- 结构比 DeepAgent 更显式，适合学习框架机制。

### 优点

- 复杂度足够，能覆盖真实 research workflow。
- 比完全自定义更稳，因为核心 plan-execute-replan 流程已有 Eino 预构建实现。
- 更容易测试每个边界：计划生成、搜索工具、执行结果、重规划、最终输出。
- 后续可以替换 Planner/Replanner prompt 或 Plan 结构，而不必重写整体流程。

### 缺点

- 内置 Plan 默认只有 `steps []string`，如果要强制每步包含搜索目标、预期来源和完成标准，需要自定义 Plan。
- 并行研究能力不是默认重点，第一版更偏串行迭代。
- 最终 JSON 输出需要我们在外层做规范化，不能完全依赖模型自然语言。

### 适配度

推荐作为第一版主路径。

## 路径二：基于 Eino `prebuilt/deep`

### 形态

使用 Eino 内置 Deep agent：

1. Deep agent 自带任务工具和 todo 工具。
2. 可以配置 search 工具、子 agent 和 MaxIteration。
3. 主 agent 根据任务需要把子任务交给 general-purpose 或自定义子 agent。
4. 最终由主 agent 综合输出。

### 学习价值

- 能体验 Eino 更高层的 DeepAgent 风格。
- 能看到 task tool、sub-agent orchestration、todo middleware 的使用方式。
- 更接近“通用复杂任务 agent”的产品体验。

### 优点

- 高层能力更完整，内置了任务拆分、todo 和子 agent 协作能力。
- 对开放式复杂任务更灵活。
- 后续如果要加入文件读写、shell 等工具，有现成 middleware 入口。

### 缺点

- 抽象层更高，学习时不如 planexecute 透明。
- 行为更模型驱动，测试难度更高。
- 容易做成“黑盒能跑”，但不利于第一版建立清晰的工程边界。
- 对当前 research agent 的结构化输出和可控研究步骤，需要额外约束。

### 适配度

适合作为后续增强或对比实现，不建议作为第一版唯一主路径。

## 路径三：自定义 ADK Workflow

### 形态

自己组合 Eino ADK primitives：

1. Planner agent 生成自定义结构化计划。
2. 多个 Researcher agent 按子问题搜索，可以用 ParallelAgent 并行。
3. Critic 或 Verifier agent 检查来源覆盖、重复和冲突。
4. Synthesizer agent 输出最终 Markdown / JSON。

### 学习价值

- 学习最深入，会接触 Eino 的基础构件和组合方式。
- 可以明确实践 Sequential、Parallel、Loop、ChatModelAgent、Tool、Runner、Session 等能力。

### 优点

- 控制力最强。
- 可以自然支持并行研究、来源评分、引用检查和复杂输出结构。
- 最贴合一个严肃 deep research 产品的长期形态。

### 缺点

- 第一版实现量最大。
- 设计和测试成本高，容易在基础 CLI 还没稳定时引入过多变量。
- 需要我们自己定义更多状态结构、错误处理和执行策略。

### 适配度

适合第二阶段。当 `prebuilt/planexecute` 版跑通后，再把关键环节替换成自定义 workflow。

## 推荐路径

第一版建议走“路径一增强版：基于 Eino `prebuilt/planexecute`，但自定义并行 Executor”，同时在工程结构上预留向“路径三：自定义 ADK Workflow”演进的空间。

具体策略：

- 第一版使用 Eino 内置 Planner/Replanner 和 `planexecute.New` 外层组合，避免一开始重写 orchestration。
- 第一版使用自定义 `ResearchPlan`，替代默认的 `steps []string`，以便表达更明确的研究步骤。
- 第一版不直接使用默认 `planexecute.NewExecutor` 作为唯一执行器，而是在 Executor 阶段引入并行 researcher。
- 自己实现 search provider、Eino `web_search` tool、配置加载、CLI、Markdown/JSON renderer。
- Executor 阶段针对当前计划步骤启动多个 researcher agent，并行调用 `web_search` 工具，再由 synthesis 步骤合并为单个 step result。
- 自定义 Executor 需要把合并后的 step result 写入 `planexecute.ExecutedStepSessionKey`，以便 Replanner 继续使用 Eino 预构建流程。
- 明确限制最大循环次数、最大搜索次数、每次搜索结果数和超时。
- 输出层在 agent 之外做规范化，保证 CLI 和未来 HTTP 服务可以复用。

这样既能学习 Eino 复杂 agent workflow，又不会让第一版失控。

## 在方案 A 中加入并行 Researcher 的方式

方案 A 的原始形态是直接使用 Eino `prebuilt/planexecute` 的 Planner、Executor、Replanner。现在需要保留外层 plan-execute-replan，但增强 Executor。

### 方式一：工具内部并行搜索

`web_search` 工具接收多个 query，在工具内部并发调用 Google Custom Search 或 Mock Search，然后返回合并结果。

优点是实现简单，测试稳定。缺点是这只是并行搜索，不是并行 researcher；模型层面仍然只有一个 Executor agent。

### 方式二：自定义并行 Executor

保留 `planexecute.New` 的外层组合，但传入自定义 Executor。该 Executor 针对当前 `ResearchStep` 启动多个 researcher agent，例如：

- `background_researcher`：查背景、定义和上下文。
- `evidence_researcher`：查数据、案例和来源。
- `counterpoint_researcher`：查反例、争议和限制。

这些 researcher 通过 `adk.NewParallelAgent` 并行运行。并行结果再交给 synthesis agent 合并成当前步骤的结果。最后自定义 Executor 将合并结果写入 `planexecute.ExecutedStepSessionKey`。

优点是保留 Eino 预构建 planexecute 的主流程，同时实际使用 ParallelAgent 和多 agent 协作。缺点是 Executor 需要自定义，测试和状态管理比默认 Executor 更复杂。

### 方式三：完全自定义 Workflow

直接放弃 `prebuilt/planexecute`，自己组合 Planner、Parallel Researchers、Verifier、Synthesizer、Replanner。

优点是控制力最高。缺点是第一版范围过大，不符合“先复用，后迭代”的决策。

### 当前推荐

选择方式二：自定义并行 Executor。

它符合两个目标：第一，继续复用 Eino `prebuilt/planexecute` 的外层复杂 agent 模板；第二，在第一版就实践 `ParallelAgent` 和多 researcher 协作。

## 并行 Researcher 角色设计依据

最初提出的三个 researcher 角色是：

- `background_researcher`：查背景、定义和上下文。
- `evidence_researcher`：查数据、案例和来源证据。
- `counterpoint_researcher`：查反例、争议和限制。

这不是行业统一标准，而是一个适合第一版 CLI 的“研究视角拆分”。选择它们的原因是：

1. 覆盖面互补：背景负责建立语境，证据负责支撑结论，反方负责降低确认偏误。
2. 适配任意主题：无论问题是技术选型、市场分析、事实核验还是产品比较，这三个视角都能工作。
3. 并行价值明确：三个角色不会只是在不同关键词上重复搜索，而是从不同问题意识出发检索。
4. 输出容易合成：三个结果天然对应报告中的背景、主要发现、风险/限制部分。
5. 工程范围可控：固定三个角色比让 Planner 动态生成任意数量 agent 更容易测试和限额。

## 与主流 Deep Research 实现的差异

公开资料显示，主流 Deep Research 产品和开源实现更常见的是“阶段角色”或“动态子任务角色”，而不是固定的背景/证据/反方三角色。

### OpenAI Deep Research

OpenAI 对 ChatGPT Deep Research 的公开说明强调的是计划、研究、综合和带引用的报告。用户可以选择来源，系统会生成可审阅的研究计划，最后输出结构化报告和来源链接。OpenAI API 文档则更偏模型和工具能力：deep research 模型能使用 web search、remote MCP、file search 和 code interpreter，并在输出中暴露搜索、代码执行、MCP、文件搜索等调用记录。

差异：OpenAI 公开文档没有暴露固定 sub-researcher 角色。它更像模型内部完成多步研究，我们能看到工具调用和最终消息，但看不到明确的 background/evidence/counterpoint 分工。

### Anthropic Research

Anthropic 公开工程文章描述了 orchestrator-worker 模式：LeadResearcher 规划策略，然后创建多个 specialized subagents 并行搜索不同方面，之后由 LeadResearcher 综合；最后还有 CitationAgent 处理引用。Anthropic 还提到复杂研究常用 3-5 个并行 subagents，subagents 也会并行使用多个工具。

差异：Anthropic 的 subagent 角色是由 LeadResearcher 根据任务动态创建的，重点是“不同方面”或“不同子任务”，不是固定三种视角。它还显式包含 CitationAgent，而我们第一版只在结果结构中保留 sources，不单独做 citation agent。

### LangChain Open Deep Research

LangChain Open Deep Research 使用 Scope、Research、Write 三阶段。Research 阶段由 supervisor agent 判断是否把 research brief 拆成独立子主题，并把任务分配给 sub-agents。每个 sub-agent 聚焦一个子主题，独立使用搜索工具或 MCP 工具；最后系统清理 sub-agent findings，再由写作阶段一次性生成报告。LangChain 明确建议只把 multi-agent 用在 research 阶段，而不是并行写报告，因为并行写作容易导致报告割裂。

差异：LangChain 的角色定义更接近“Supervisor + subtopic researchers + writer”。我们的三角色是“同一个 ResearchStep 下的三个研究视角”。它更固定、更简单，但不如 supervisor 动态拆分灵活。

### Google Gemini Deep Research

Gemini Deep Research 的公开帮助文档强调来源选择、生成研究计划、用户可编辑计划、开始研究、生成报告。公开文档没有给出内部 agent 角色结构。

差异：Gemini 对外呈现的是产品流程和用户控制点，不暴露 subagent 角色。我们的设计会把角色和步骤显式记录到 JSON，学习和调试更透明。

### Perplexity Sonar Deep Research

Perplexity Sonar Deep Research 文档强调 exhaustive search、hundreds of sources、reasoning effort、async API、citations、search query 数量和 reasoning token 成本。公开 API 也不暴露内部 agent 角色结构。

差异：Perplexity 是模型/API 产品形态，暴露成本、引用和搜索统计，不暴露 planner/researcher/synthesizer 等内部角色。我们的实现会显式建模这些角色，便于学习 Eino。

## 对三角色设计的修正建议

固定的 `background_researcher`、`evidence_researcher`、`counterpoint_researcher` 适合第一版，但它们不应该被当作唯一长期形态。

更稳妥的第一版设计是：

- 保留三个默认 researcher role，确保 CLI 有稳定行为。
- 允许每个 `ResearchStep` 带 `research_axes` 或 `search_queries`，让角色根据当前 step 调整关注点。
- 在 JSON 输出中记录每个 researcher 的 role、focus、queries、findings、sources。
- 后续迭代时，把固定三角色升级为 Planner 动态生成 researcher specs。

这样第一版有足够学习价值和可控性，后续又能向 Anthropic / LangChain 那类动态 subagent delegation 演进。

## `prebuilt/planexecute` 与自定义 ADK Workflow 的核心差异

| 维度 | `prebuilt/planexecute` | 自定义 ADK Workflow |
| --- | --- | --- |
| 抽象层级 | Eino 已封装好的 plan-execute-replan 模式 | 直接组合 Eino ADK 基础构件 |
| 主要组件 | Planner、Executor、Replanner、SequentialAgent、LoopAgent | 自定义 Planner、Researcher、Verifier、Synthesizer，可组合 Sequential/Parallel/Loop |
| 控制力 | 中等，核心循环由 Eino 预构建实现决定 | 最高，流程、状态、并行策略和退出条件都自己定义 |
| 学习重点 | 学习 Eino 官方预构建复杂 agent 的用法和扩展点 | 学习 Eino ADK primitives 的底层组合方式 |
| 实现速度 | 更快，第一版更容易跑通 | 更慢，需要自己设计 orchestration 和状态结构 |
| 工程风险 | 较低，主流程已有框架实现 | 较高，容易在流程控制、状态传递和错误处理上踩坑 |
| 测试难度 | 中等，可以围绕 provider、tool、renderer 和配置做稳定测试 | 高，需要测试更多自定义 agent 交互和状态流转 |
| 并行研究 | 不是默认重点，第一版更偏串行 plan-execute-replan | 可以自然设计为多 researcher 并行搜索 |
| 结构化计划 | 默认 Plan 较简单，需要扩展才能表达复杂研究元数据 | 可以从一开始定义完整研究计划结构 |
| 输出可控性 | 需要外层 renderer 和规范化步骤兜底 | 可以把输出规范化设计成 workflow 的显式阶段 |
| 适合阶段 | 第一版主路径 | 第二阶段增强路径 |

### 一个直观类比

`prebuilt/planexecute` 像是使用 Eino 提供的“标准复杂 agent 模板”：你能看到计划、执行、重规划如何协作，也能接入自己的工具，但不需要自己从零写循环控制。

自定义 ADK Workflow 像是自己搭一个 research agent 框架：你可以设计并行研究员、验证员、综合员、状态结构和退出策略，但第一版要承担更多设计和调试成本。

### 对当前项目的影响

如果第一版使用 `prebuilt/planexecute`：

- 我们会优先学习和使用 Eino 官方复杂 agent 组合。
- 我们仍然会自己实现 `web_search` 工具、搜索 provider、CLI、配置、输出 renderer。
- 第一版更容易形成一个可以真实运行和测试的 deep research CLI。
- 后续可以逐步替换 Planner、Executor、Replanner 的 prompt 或 Plan 结构。

如果第一版直接使用自定义 ADK Workflow：

- 我们会学到更多底层组合方式。
- 可以直接支持并行研究和更细的状态模型。
- 但要先设计更多基础设施，第一版交付面会明显变大。
- 测试成本和不确定性也会更高。

因此当前建议不是“永远不用自定义 Workflow”，而是先用 `prebuilt/planexecute` 建立可运行基线，再有目标地替换成自定义 Workflow。
