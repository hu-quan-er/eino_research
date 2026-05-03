# Research Agent 对话记录

日期：2026-05-03

本文记录 Eino 版 research agent 初始产品和架构讨论中的已确认决策、待确认事项和背景信息。后续讨论记录默认使用中文维护。

## 当前上下文

- 工作目录：`/Users/seaya/huquan.95/project-4/research`
- 讨论开始时，当前目录为空。
- 讨论开始时，当前目录不是 git 仓库。
- 本地 Go 版本：`go1.26.1 darwin/amd64`
- 本地模块缓存中已有：
  - `github.com/cloudwego/eino@v0.8.13`
  - `github.com/cloudwego/eino-ext/components/model/openai@v0.1.13`

## 已确认决策

- 基于 Eino 框架实现一款基础 research agent。
- 第一版入口：CLI。
- 后续扩展目标：HTTP 服务。
- 搜索 provider：
  - 默认保留 Mock Search，用于本地稳定测试。
  - 真实搜索优先接入 Google Custom Search。
  - Google provider 使用 `GOOGLE_API_KEY` 和 `GOOGLE_CSE_ID`。
  - 第一版不优先接 Bing。原因是传统 Bing Search APIs 已在 2025-08-11 退役；新的 Microsoft Grounding with Bing Search 更偏 Azure Agent 场景，不是简单的原始搜索结果 API。
- 模型 provider：
  - 使用 OpenAI-compatible 配置。
  - 环境变量包括 `OPENAI_API_KEY`、`OPENAI_MODEL`，以及可选的 `OPENAI_BASE_URL`。
- 输出：
  - 默认输出 Markdown 研究报告。
  - 通过 CLI flag，例如 `--json`，可输出结构化 JSON。
- 记录语言：
  - 后续讨论记录默认使用中文维护。

## 已确认研究深度

第一版 CLI 的研究深度选择：

- 方案一：单轮搜索 + 总结。
- 方案二：ReAct 多轮搜索 Agent。
- 方案三：Deep Research / Plan-Execute Agent。已选择。

选择原因：用户希望借这个项目熟悉 Eino 框架的实际使用，并希望覆盖更复杂的 research agent 场景，因此第一版不走最轻量路径，而是直接做 Deep Research / Plan-Execute 风格。

## 已记录材料

- 三种研究深度方案对比：`docs/discussion/research-depth-options.md`
- Deep Research 架构落地路径对比：`docs/discussion/deep-research-architecture-options.md`
- 当前对话和决策记录：`docs/discussion/conversation-log.md`

## 当前推荐实现路径

在已选择方案三的前提下，第一版确认基于 Eino `prebuilt/planexecute` 实现，而不是直接使用 `prebuilt/deep` 或完全自定义 workflow。

理由：`prebuilt/planexecute` 能显式展示 Planner、Executor、Replanner、SequentialAgent 和 LoopAgent 的组合方式，适合学习 Eino 的复杂 agent workflow；同时它比完全自定义 workflow 更容易控制第一版范围。

已补充材料：`docs/discussion/deep-research-architecture-options.md` 中新增了 `prebuilt/planexecute` 与自定义 ADK Workflow 的核心差异对比。

后续演进方向：第一版先复用 `prebuilt/planexecute` 建立可运行基线，后续再逐渐迭代到自定义 ADK Workflow，例如加入并行 Researcher、Verifier、Synthesizer 和更细的研究状态模型。

## 已确认 Plan 结构

第一版选择自定义 `ResearchPlan`，而不是直接使用 Eino 默认的 `steps []string`。

初步方向：每个研究步骤包含 `id`、`title`、`question`、`search_queries` 和 `success_criteria` 等字段。这样既能继续复用 `prebuilt/planexecute` 的 orchestration，也能学习 Eino 对自定义 Plan 的扩展方式，并让 deep research 的步骤更可控、可测试、可追踪。

## 已确认 JSON 输出结构

第一版 `--json` 输出选择“研究过程 JSON”，包含 `question`、`answer`、`plan`、`executed_steps`、`sources` 和 `metadata`。

该结构既能体现 Deep Research 的计划和执行过程，也避免输出完整 agent events / tool calls trace 带来的噪声和体积问题。

## 已确认并行研究要求

第一版需要在方案 A 中引入并行 researcher。

设计方向：继续复用 Eino `prebuilt/planexecute` 的 Planner / Replanner 和外层 plan-execute-replan 编排，但不直接使用默认 `planexecute.NewExecutor` 作为唯一执行器。Executor 阶段改为自定义“并行研究执行器”：针对当前计划步骤启动多个 researcher agent 并行搜索和分析，再由 synthesis 步骤合并为该步骤的执行结果，并写入 `planexecute.ExecutedStepSessionKey`，供 Replanner 判断是否继续。

已补充角色设计依据：`docs/discussion/deep-research-architecture-options.md` 中新增了并行 researcher 三角色的选择原因，以及它们与 OpenAI、Anthropic、LangChain、Gemini、Perplexity 公开 Deep Research 形态的差异。

当前已确认：第一版固定使用 `background_researcher`、`evidence_researcher`、`counterpoint_researcher` 这三个并行 researcher 角色。它们只作为第一版默认角色，不作为长期唯一形态。`ResearchPlan` 中应预留 `research_axes` 或类似字段，后续可以升级为 Planner 动态生成 researcher specs。

## 已确认配置管理

第一版需要提供基础配置文件，用于统一管理模型、搜索、执行限制和输出相关配置。

配置优先级：CLI flags > 环境变量 > 配置文件 > 默认值。

建议默认配置路径：`research.yaml`，并支持 `--config path/to/config.yaml` 指定配置文件。

环境变量仍然用于敏感信息覆盖，例如 `OPENAI_API_KEY`、`GOOGLE_API_KEY`。配置文件可以引用非敏感默认值，例如模型名、base URL、provider、max iterations、search limits、timeout 和输出模式。

## 来源说明

- Google Custom Search JSON API 需要 API key 和 Programmable Search Engine ID。
- Microsoft 宣布传统 Bing Search APIs 于 2025-08-11 退役。
- Microsoft 当前推荐的 Bing grounding 路径是 Azure AI Foundry / Agent Service Grounding with Bing Search。
