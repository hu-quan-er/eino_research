// Package search 定义 research 引擎依赖的最小搜索 provider 边界。
//
// provider 统一返回 Source，后续执行、证据归一化和报告渲染都只依赖这个结构，
// 避免把 Google Custom Search 等具体 API 细节扩散到 research 核心逻辑中。
//
// 当前跳转路径是 research.NewWebSearchTool 调用 Provider.Search，随后 RankSources 负责
// 规则排序，research 层再重写 source_id 并绑定到 findings/evidence_refs。
package search
