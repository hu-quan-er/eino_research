# 架构历史债清理记录

## 背景

项目早期使用 Eino `prebuilt/planexecute`、自定义 `ResearchPlan` 和固定三角色并行 executor 作为第一版实现。后续主流程已经演进为：

1. `Runner.Plan` 生成 `ResearchTodoPlan`。
2. `Runner.Execute` 使用 `TodoScheduler` 按依赖调度 todo。
3. 单个 todo 内由 `TodoDispatcher` 派发 researcher job，并通过 bounded research loop 补充 gap。

继续保留旧 `ResearchPlan` / `planexecute` 路径会带来两个问题：第一，调用方不容易判断 `Run`、`Plan`、`Execute` 哪条才是主路径；第二，配置、结果结构和测试里持续出现 legacy 字段，后续做异常处理、重试和证据链路优化时边界会变得模糊。

## 本次选型

本次选择直接收敛到 todo-plan 主流程，而不是继续保留双路径兼容。

保留内容：

- `ResearchTodoPlan` 作为唯一 plan schema。
- `ResearchStep` 作为 todo 内部执行器的中间结构，用于复用 `ParallelStepExecutor`、`AgentResearcher` 和 `AgentSynthesizer`。
- `Runner.Plan` + `Runner.Execute` 作为可人工确认计划的主调用方式。
- `Runner.Run` 作为 `Plan` + `Execute` 的便捷组合。

移除内容：

- 旧 `ResearchPlan` 类型和相关 JSON/Validate/FirstStep 逻辑。
- 旧 `EinoParallelExecutor` ADK adapter。
- 旧 `planexecute` planner/replanner 组装逻辑。
- `ResearchResult.LegacyPlan`、`ResearchResult.ExecutedSteps`。
- `research.max_iterations`、`--max-iterations`、`researcher_roles` 等旧配置入口。

## 影响

当前项目的架构边界变为：

- Planner 只负责输出 todo plan。
- Scheduler 只负责 todo 依赖调度。
- Dispatcher 只负责把 todo 派发成角色化 researcher jobs。
- Executor 只负责执行单个 todo 内的 researcher fan-out、synthesis 和 bounded retry。
- Result 只表达 todo-plan 主流程的结果，不再混入 legacy step-plan 输出。

这为后续优化留下了更清晰的落点：结构化 researcher/synthesizer 输出、每轮 retry 独立工具预算、最终全局 answer synthesis、证据链路归一化，都可以在当前主流程上继续推进。
