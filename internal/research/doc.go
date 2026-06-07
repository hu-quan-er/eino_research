// Package research 实现 deep research 的核心流程。
//
// 这里包含 planner 输出校验、todo 调度、子 researcher 派发、工具调用、证据归一化、
// bounded retry loop，以及最终 ResearchResult 聚合逻辑。
//
// 主要跳转路径是 Runner.Plan -> Runner.Execute -> TodoScheduler -> defaultTodoExecutor
// -> ParallelStepExecutor -> FinalSynthesizer -> EvidenceBinder -> ClaimVerifier。外部调用方
// 通常只需要依赖 Runner、ResearchTodoPlan 和 ResearchResult。
package research
