// Package render 负责把结构化 ResearchResult 转换为用户可读或机器可读的输出。
//
// JSON 输出保留完整结果结构；Markdown 输出面向终端阅读，并在有 evidence_refs /
// documents 时尽量展示 claim 级别的引用证据。
package render
