# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-05-24

Minor release consuming `rag.Observer.OnGenerateUsage` (introduced in
`llm-agent-rag` v1.5.0) to emit per-stage token usage as OTel metrics.

### Added

- `otelrag.MetricGenerateTokens` constant = `"rag.generate.tokens"` —
  new Int64Counter recording per-Generate token consumption inside the
  RAG pipeline.
- `otelrag.AttrStage` = `"rag.stage"`, `otelrag.AttrEstimated` =
  `"rag.estimated"` — new attribute key constants.
- `otelrag.Observer(cfg)` factory now populates `rag.Observer.OnGenerateUsage`
  to record `rag.generate.tokens` with attributes
  `{rag.stage, rag.token.kind=prompt|completion, rag.estimated}`.
- `otelrag.MakeOnGenerateUsageHook(mp metric.MeterProvider) func(ctx,
  stage string, usage obs.TokenUsage)` — standalone factory for callers
  who construct `rag.Observer` manually (escape hatch).

### Changed

- (none — fully additive)

### Compatibility

- Existing `rag.tokens` metric is **unchanged**. `rag.generate.tokens` is
  a separate metric; queries against either are independent. Combining
  them in a dashboard would double-count the answer leg — filter by
  attribute set instead.
- `rag.generate.tokens` emits only `prompt` and `completion` kinds, not
  `total` (avoid double-count).
- `otelrag.Wrap(*rag.System, Config)` is **unchanged** — does NOT
  auto-install OnGenerateUsage. Callers must construct `rag.Observer`
  with the hook explicitly (via `Observer(cfg)` factory or
  `MakeOnGenerateUsageHook`) before calling `rag.New`. This limitation
  exists because `*rag.System` does not expose its Observer for
  post-construction mutation.
- OTel SDK v1.43.0 synchronous instruments are concurrent-safe — the
  hook may fire from parallel goroutines under v1.2.1 ParallelFollowups
  or v1.4.0 AnswerBenchmark.Parallelism>=2.
- Per-stage tokens here are independent of any `otelmodel` token
  attribution on the underlying `llm.ChatModel` — they answer different
  questions (RAG-pipeline-stage cost vs raw model-call cost).
- No new dependencies. Stays on existing OTel SDK pin.
