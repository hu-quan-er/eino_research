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

- 当前 auto-todo 的直接执行路径仍主要使用 search provider，下一步可以把 `web_fetch` 接入 todo executor，对搜索结果自动深读。
- PDF 和网页正文应统一为 source document 模型，方便后续 claim-level citations。
- 长网页需要 chunking，否则单次工具返回仍可能丢失关键段落。
