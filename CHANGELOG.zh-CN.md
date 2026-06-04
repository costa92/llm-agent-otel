# Changelog

本项目的所有重要变更都记录于此。格式遵循
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，且本项目
遵守[语义化版本](https://semver.org/spec/v2.0.0.html)。

## [0.3.0] - 2026-05-24

消费 `rag.Observer.OnGenerateUsage`（在 `llm-agent-rag` v1.5.0 中引入）
以将每阶段 token 用量作为 OTel 指标发出的次要发布。

### Added

- `otelrag.MetricGenerateTokens` 常量 = `"rag.generate.tokens"` ——
  新的 Int64Counter，记录 RAG 流水线内每次 Generate 的 token 消耗。
- `otelrag.AttrStage` = `"rag.stage"`、`otelrag.AttrEstimated` =
  `"rag.estimated"` —— 新的属性键常量。
- `otelrag.Observer(cfg)` 工厂现在会填充 `rag.Observer.OnGenerateUsage`，
  以记录带属性
  `{rag.stage, rag.token.kind=prompt|completion, rag.estimated}` 的
  `rag.generate.tokens`。
- `otelrag.MakeOnGenerateUsageHook(mp metric.MeterProvider) func(ctx,
  stage string, usage obs.TokenUsage)` —— 面向手动构造
  `rag.Observer` 的调用方的独立工厂（逃生舱）。

### Changed

- （无 —— 完全仅增量）

### Compatibility

- 既有的 `rag.tokens` 指标**保持不变**。`rag.generate.tokens` 是
  一个独立的指标；针对两者中任一的查询都是独立的。在仪表盘中
  合并它们会对答案环节重复计数 —— 请改为按属性集合过滤。
- `rag.generate.tokens` 只发出 `prompt` 和 `completion` 两种 kind，不发出
  `total`（避免重复计数）。
- `otelrag.Wrap(*rag.System, Config)` **保持不变** —— 不会
  自动安装 OnGenerateUsage。调用方必须在调用 `rag.New` 之前显式地用
  钩子构造 `rag.Observer`（通过 `Observer(cfg)` 工厂或
  `MakeOnGenerateUsageHook`）。此限制
  之所以存在，是因为 `*rag.System` 不会暴露其 Observer 以供
  构造后修改。
- OTel SDK v1.43.0 的同步 instrument 是并发安全的 —— 该
  钩子可能在 v1.2.1 ParallelFollowups 或 v1.4.0
  AnswerBenchmark.Parallelism>=2 下从并行 goroutine 触发。
- 此处的每阶段 token 独立于底层 `llm.ChatModel` 上 `otelmodel` 的任何
  token 归属 —— 它们回答不同的
  问题（RAG 流水线阶段成本 vs 原始模型调用成本）。
- 无新依赖。仍保持现有的 OTel SDK 锚定。
