// Package render 负责把结构化 ResearchResult 转换为用户可读或机器可读的输出。
//
// JSON 输出保留完整结果结构；Markdown 输出面向终端阅读，并在有 evidence_refs /
// documents 时尽量展示 claim 级别的引用证据。
//
// Markdown 渲染不会重新推理研究内容，只按 Answer、TodoExecutions、Sources 和 Documents
// 做格式化与兜底查找，确保渲染层不引入新的事实判断。
package render
