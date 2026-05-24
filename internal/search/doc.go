// Package search 定义 research 引擎依赖的最小搜索 provider 边界。
//
// provider 统一返回 Source，后续执行、证据归一化和报告渲染都只依赖这个结构，
// 避免把 Google Custom Search 等具体 API 细节扩散到 research 核心逻辑中。
package search
