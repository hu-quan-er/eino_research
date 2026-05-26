# Deep Research 效果优化路线记录

本文记录结合 WebGPT、ReAct、STORM、MindSearch、Self-RAG、RARR、CoVe、ALCE、HyDE、FLARE、ColBERT、RAPTOR、GraphRAG 等论文思路，以及主流 deep research 产品形态后，当前项目的效果优化顺序和选型。

## 总体判断

当前项目已经具备 todo plan、依赖调度、多 researcher 并行、web_search/web_fetch、todo 内 synthesis、gap retry 和 evidence refs 的基础能力。下一阶段提升效果的重点不是单纯增加搜索次数或 researcher 数量，而是增强以下闭环：

- 全局综合：把所有 todo 的结果综合成真正的最终报告。
- 引用绑定：让最终答案中的关键 claim 能回到具体 source/chunk。
- 动态派发：不同 todo 派发不同视角和角色，而不是长期固定三角色。
- 动态检索：根据研究进展扩展 query 和二次检索。
- 来源排序：优先使用更权威、更相关、更可核验的来源。
- 结论验证：对最终答案的关键 claim 做独立验证和修订。
- 评测闭环：用固定问题集衡量改动是否真的提升效果。

## 落地优先级

1. `FinalSynthesizer`
2. `CitationAgent / Evidence Binder`
3. `DynamicTodoDispatcher`
4. `Query Expansion / Retrieval Strategy Planner`
5. `Source Ranker / Reranker`
6. `Claim Verification`
7. `Eval Harness`

这个顺序优先解决“最终用户看到的答案质量”。如果没有全局综合和最终引用绑定，即使 todo 层收集了很多证据，最终报告仍然会显得像执行日志，而不是 deep research 报告。

## 方案选型对比

### 1. FinalSynthesizer

可选方案：

- 继续由 renderer 拼接 todo summary：稳定、无需模型调用，但只能形成执行日志，无法跨 todo 归纳。
- 每个 section 单独综合后再总综合：结构更清晰，但第一版会引入更多接口和中间结构。
- 一个全局 final synthesizer：实现成本低，能直接提升最终报告质量。

当前选择：

- 先实现一个全局 `FinalSynthesizer`。
- 输入 question、plan、section executions、todo executions、sources、documents。
- 输出 `Answer.markdown`、`summary`、`key_findings`、`limitations`。
- 默认实现为 `AgentFinalSynthesizer`，同时保留接口，便于后续替换成 section-level synthesis 或更强的 final verifier。

已落地：

- `RunnerConfig.FinalSynthesizer`
- `FinalSynthesisInput`
- `AgentFinalSynthesizer`
- `Runner.Execute` 在 todo 聚合后调用最终综合器。
- 最终综合失败时，基于 todo/findings/gaps 生成确定性兜底 answer。

### 2. CitationAgent / Evidence Binder

可选方案：

- 仅要求 final synthesizer 在 Markdown 中写 source id：实现简单，但无法保证每条 claim 都被证据支撑。
- 后处理绑定 evidence：把 final answer 拆成 claim，再从 `SourceDocument.Chunks` 里找支撑证据。
- 独立 citation agent：输入 final answer 和 documents，输出带 citation 的修订版报告。

建议选择：

- 下一步先做“后处理 Evidence Binder”，代码控制 claim/chunk 映射，减少模型自由发挥。
- 后续再把 citation agent 作为增强，而不是第一版直接依赖模型完成全部引用绑定。

预期接口：

```go
type EvidenceBinder interface {
    BindEvidence(ctx context.Context, in EvidenceBindingInput) (Answer, []ClaimEvidence, error)
}
```

核心行为：

- 从 `Answer.Markdown` 或 `Answer.KeyFindings` 提取关键 claim。
- 用 source id、关键词和 chunk 文本做候选匹配。
- 找不到证据的 claim 标记为 unsupported，并移入 limitations 或降低语气。
- 输出 claim -> evidence_refs 的结构化映射。

已落地第一版：

- `RunnerConfig.EvidenceBinder`
- `EvidenceBindingInput`
- `RuleBasedEvidenceBinder`
- `Answer.Evidence`
- Markdown 新增 `Answer Evidence` 段落。

当前第一版是确定性规则实现，不调用模型：

- 优先匹配 final answer 中显式写出的 source id。
- 如果没有 source id，则用文本 overlap 从 `SourceDocument.Chunks` 和 todo findings 中找证据。
- unsupported claim 会追加到 `Answer.Limitations`。

后续增强：

- 把 answer paragraph 拆成更细粒度 claim，而不只依赖 key findings 和 Markdown 行。
- 引入可选模型 CitationAgent，对 unsupported 或低置信 claim 做重写。
- 对 evidence quote 做更强的相关性评分，避免只因关键词重合而误绑定。

### 3. DynamicTodoDispatcher

可选方案：

- 固定角色：稳定，但对不同 todo 的适配性差。
- 规则路由：根据 todo 类型、关键词、acceptance criteria 选择角色，稳定且可测试。
- 模型派发：最灵活，但必须校验输出，且可能带来格式错误和角色漂移。

建议选择：

- 先升级到“规则路由 + 可插拔模型派发”。
- 默认仍可用规则，复杂 todo 可启用模型派发。
- 模型只输出 `TodoResearchJob[]`，不得改 plan 和依赖。

已落地第一版规则增强：

- `RuleBasedTodoDispatcher` 保持原接口，不引入模型派发。
- 派发时读取 todo title、question、search queries、acceptance criteria 和 dependency executions。
- 支持动态加入：
  - `implementation_researcher`
  - `comparison_researcher`
  - `quantitative_researcher`
  - `freshness_researcher`
  - 依赖存在 gap/error 时的 `gap_checker`
- 仍然受 `research.max_researchers_per_todo` 约束，避免角色扩展导致 fan-out 失控。

后续增强：

- 增加 role priority 配置，让不同业务场景可以调整角色截断顺序。
- 增加可选模型 dispatcher，但必须走 schema 校验，只允许返回 `TodoResearchJob[]`。
- 把 acceptance criteria 映射为更明确的 job success criteria。

### 4. Query Expansion / Retrieval Strategy Planner

可选方案：

- planner 一次性给 search queries：简单，但覆盖不足。
- researcher 内自主生成 query：灵活，但不易观测。
- 单独 query planner：先生成多类 query，再交给 researcher 使用。

建议选择：

- 给 researcher 前置一个轻量 query expansion 阶段。
- query 类型至少包括 keyword、entity、official/source-scoped、freshness、counterexample。
- 每个 todo 记录实际 query 覆盖，便于后续 eval。

已落地第一版：

- 新增 `ExpandTodoSearchQueries`。
- `todoToResearchStep` 会把 planner 原始 `search_queries` 扩展后写入 `ResearchStep.SearchQueries`。
- 当前扩展类型包括：
  - official documentation
  - GitHub/repository/examples
  - latest/2026
  - alternatives/comparison/tradeoffs
  - benchmark/performance/pricing/metrics
  - limitations/risks/counterexamples

当前选择仍是确定性扩展，不额外调用模型。这样能先提高检索覆盖，同时保持测试可控。

后续增强：

- entity extraction：从问题中抽取产品、机构、论文、标准、版本号等实体，生成实体 query。
- domain-scoped query：针对 docs、GitHub、arXiv、标准组织、官方博客等生成 site/domain scoped query。
- query coverage 记录：把实际使用的 query 和未使用 query 记录到 todo execution。

### 5. Source Ranker / Reranker

可选方案：

- 规则打分：按来源权威性、时效性、可抓取性、重复度排序，简单稳定。
- embedding/reranker：相关性更强，但需要额外依赖。
- ColBERT 类 late interaction：效果好，但工程复杂度更高。

建议选择：

- 先做规则 ranker，并预留 reranker 接口。
- 排序信号包括官方来源、论文/标准、发布日期、是否 fetch 成功、domain 可信度、query 相关性。

已落地第一版：

- 新增 `search.RankSources`。
- `search.Source` 新增 `rank_score` 和 `rank_reason`。
- `web_search` 在返回模型前先对 provider 结果打分排序，再重写 source id。
- 当前排序信号包括：
  - HTTPS
  - snippet 是否存在
  - docs/official documentation
  - scholarly domain
  - standards/public domain
  - GitHub
  - query token overlap

后续增强：

- 接入 fetch 成功率和正文长度作为 rank 信号。
- 增加 freshness/date 解析。
- 预留 embedding/reranker 接口，对高价值 query 做二阶段精排。

### 6. Claim Verification

可选方案：

- final synthesizer 自己检查：简单，但容易自证。
- CoVe 风格：生成验证问题，独立回答，再修订最终答案。
- RARR 风格：对答案进行 retrieval-augmented revision。

建议选择：

- 在 Evidence Binder 后加入 claim verification。
- 只验证最终答案中的关键 claim，不验证所有中间 finding。
- 验证失败的 claim 要么删除，要么移入 limitations。

已落地第一版：

- 新增 `RunnerConfig.ClaimVerifier`
- 新增 `ClaimVerificationInput`
- 新增 `RuleBasedClaimVerifier`
- `Runner.Execute` 在 `EvidenceBinder` 后调用 verifier。

当前第一版不使用模型 judge：

- 基于 `Answer.Evidence` 判断 claim 是否 supported。
- unsupported claim 会从 `Answer.KeyFindings` 移除，并写入 `Answer.Limitations`。
- 只有 source-level、缺少 chunk/quote 的 claim 会标记为弱支持。

后续增强：

- 对 unsupported claim 发起 targeted follow-up search，而不是只移入 limitations。
- 对 `Answer.Markdown` 做段落级修订，删除或降级 unsupported 表述。
- 引入可选 CoVe/RARR 风格 verifier，但需要与当前确定性结果对齐。

### 7. Eval Harness

可选方案：

- 手工看输出：成本低，但无法比较改动。
- 固定问题集 + golden criteria：可持续评估。
- 自动 judge：效率高，但 judge 本身需要校准。

建议选择：

- 先建立固定 eval 集和规则指标，暂不引入模型 judge。
- 指标包括 citation coverage、unsupported claim count、source diversity、answer completeness、freshness coverage。

已落地第一版：

- 新增 `internal/eval`。
- 新增 `eval/questions.yaml` 样例问题集。
- 支持 `LoadSuite(path)` 读取 YAML，并启用 strict field 校验。
- 支持 `EvaluateResult(result, case)` 评估单次 `ResearchResult`。
- 当前规则指标包括：
  - citation coverage
  - unsupported claim count
  - source diversity
  - required claims
  - forbidden claims
  - required source hints

后续增强：

- 增加 `cmd/research-eval` 批量执行 CLI。
- 持久化每次 eval 的 JSON 结果，支持不同提交之间对比。
- 增加 freshness coverage、citation precision 和 answer completeness 的更细规则。
- 在规则指标稳定后，再考虑可选模型 judge。

## 当前下一步

`FinalSynthesizer`、确定性 `EvidenceBinder`、规则增强版 `DynamicTodoDispatcher`、确定性 `Query Expansion`、规则 `Source Ranker`、非模型 `Claim Verification` 和最小 `Eval Harness` 已完成第一版。

下一轮建议进入第二阶段增强：

- 给 eval harness 增加 CLI 和结果持久化。
- 把 EvidenceBinder 从行级 claim 抽取升级为段落级 claim 抽取。
- 给 SourceRanker 增加 freshness/date 和 fetch-success 信号。
- 引入可选模型 CitationAgent，但保持当前确定性 binder/verifier 作为基线。
