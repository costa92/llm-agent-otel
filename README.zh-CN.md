[English](./README.md) | [简体中文](./README.zh-CN.md)

# llm-agent-otel

为 [`github.com/costa92/llm-agent`](https://github.com/costa92/llm-agent) 提供的 OpenTelemetry 装饰器包装器。它用 `gen_ai.*` semconv 感知的 span、指标和 slog 桥接来包装 `llm.ChatModel`（以及 `agents.Agent` 接口）——同时不触碰核心仓库的仅标准库不变量。

本仓库现已交付 Phase 5 可观测性表面：

- 面向 `llm.ChatModel` 的 `otelmodel.Wrap(...)`
- 面向 `agents.Agent` 的 `otelagent.Wrap(...)`
- 面向 `slog.Handler` 的 `otelslog.NewHandler(...)`
- 集中管理的 `gen_ai.*` 常量与门控
- `otelmetrics/` 中的低基数指标辅助函数
- OTLP 导出器接线默认值
- 使用 `grafana/otel-lgtm` 的 `compose/compose.yaml` 演示

## Install

```bash
go get github.com/costa92/llm-agent-otel@latest
```

## Quick start

```go
import (
    "context"
    "log/slog"

    "github.com/costa92/llm-agent"
    "github.com/costa92/llm-agent/llm"
    otel "github.com/costa92/llm-agent-otel"
    "github.com/costa92/llm-agent-otel/otelagent"
    "github.com/costa92/llm-agent-otel/otelmodel"
    "github.com/costa92/llm-agent-otel/otelslog"
)

ctx := context.Background()

tp, _ := otel.NewTracerProvider(ctx, otel.DefaultExporterConfig())
defer tp.Shutdown(ctx)

model := llm.NewScriptedLLM(llm.WithModel("demo"))
wrappedModel := otelmodel.Wrap(model, otelmodel.Config{TracerProvider: tp})
agent := agents.NewSimpleAgent(wrappedModel, agents.SimpleOptions{Name: "demo"})
wrappedAgent := otelagent.Wrap(agent, otelagent.Config{TracerProvider: tp})

logger := slog.New(otelslog.NewHandler(slog.Default().Handler(), otelslog.Options{}))
logger.InfoContext(ctx, "agent run starting", slog.String(otel.AttrSystem, "scripted"))

_, _ = wrappedAgent.Run(ctx, "hello")
```

当内部模型实现了 `ToolCaller`、`Embedder` 和 `StructuredOutputs` 时，
`otelmodel.Wrap(...)` 会予以保留。

## RAG per-stage token metrics (`otelrag` v0.3.0)

`otelrag` 为 `llm-agent-rag` v1.5.0 中交付的
`rag.Observer.OnGenerateUsage` 钩子暴露了两条接线路径。
两者都会记录到一个新的 instrument：

| Metric                  | Kind          | Attributes                                                    |
| ----------------------- | ------------- | ------------------------------------------------------------- |
| `rag.generate.tokens`   | Int64Counter  | `rag.stage`（例如 `ask`、`reflection_decision`、`grader`、`planner`）、`rag.token.kind`（`prompt`/`completion`）、`rag.estimated`（bool） |

**路径 A —— `Observer(cfg)` 工厂**（一次性覆盖全部四个钩子）：

```go
mp := otel.GetMeterProvider()
observer := otelrag.Observer(otelrag.Config{MeterProvider: mp})
sys := rag.New(rag.Options{Observer: observer /* ... */})
wrapper := otelrag.Wrap(sys, otelrag.Config{MeterProvider: mp})
```

**路径 B —— `MakeOnGenerateUsageHook` 逃生舱**（与你自己的钩子组合）：

```go
mp := otel.GetMeterProvider()
hook := otelrag.MakeOnGenerateUsageHook(mp)
observer := rag.Observer{
    OnGenerateUsage: hook,
    // ... your own OnImport / OnRetrieve / OnAsk implementations ...
}
sys := rag.New(rag.Options{Observer: observer /* ... */})
wrapper := otelrag.Wrap(sys, otelrag.Config{MeterProvider: mp})
```

`otelrag.Wrap(sys, cfg)` **不会**自动安装 `OnGenerateUsage` ——
`*rag.System` 不会暴露其 Observer 以供构造后修改，
因此该钩子必须在 `rag.New(opts)` 之前接线完毕。

`rag.generate.tokens` 独立于既有的 `rag.tokens` 计数器，
也独立于 `otelmodel` 在底层 `llm.ChatModel` 上发出的任何 token 归属。
在单个查询中合并 `rag.generate.tokens` 和 `rag.tokens` 会对答案环节
重复计数——请改为按属性集合过滤。出于同样的原因，
只会发出 `prompt` 和 `completion` 两种 kind（不发出 `total`）。

## Exporter defaults

`DefaultExporterConfig()` 目前默认使用 `http://localhost:4318` 上的 OTLP HTTP：

```go
cfg := otel.DefaultExporterConfig()
// Protocol: "http"
// Endpoint: "http://localhost:4318"
```

若想改为选择 OTLP gRPC：

```go
cfg := otel.DefaultExporterConfig()
cfg.Protocol = otel.ProtocolGRPC
cfg.Endpoint = "localhost:4317"
```

## Standard OTel env vars

`DefaultExporterConfig()` 和 `NewTracerProvider(ctx, cfg)` 都遵循
标准的 OpenTelemetry 导出器环境变量：

| Env var                              | Effect                                         |
| ------------------------------------ | ---------------------------------------------- |
| `OTEL_EXPORTER_OTLP_ENDPOINT`        | 覆盖 `Endpoint`                                |
| `OTEL_EXPORTER_OTLP_PROTOCOL`        | `grpc` → `ProtocolGRPC`；`http` / `http/protobuf` → `ProtocolHTTP` |
| `OTEL_EXPORTER_OTLP_INSECURE`        | 解析为 bool —— 覆盖 `Insecure`                 |

优先级为 **调用方 > 环境变量 > 硬编码默认值**：你交给
`NewTracerProvider` 的 `ExporterConfig` 值上任何非零字段都会被原样保留；
环境变量只填充调用方留空的字段；硬编码默认值
（`http://localhost:4318`、`ProtocolHTTP`、`Insecure: true`）仅在
调用方和环境变量都沉默时才生效。

```bash
# Switch the whole process to a remote OTLP collector without code changes
export OTEL_EXPORTER_OTLP_ENDPOINT=http://collector.prod:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_INSECURE=false
```

## Sampling

`ExporterConfig` 暴露了两个采样器控制项：

```go
cfg := otel.DefaultExporterConfig()

// Option 1: hand the SDK a fully-built Sampler. Wins over SamplingRatio.
cfg.Sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))

// Option 2: ratio-only shortcut. Values in (0, 1] become
// ParentBased(TraceIDRatioBased(ratio)); any other value falls back to
// ParentBased(AlwaysSample()), matching the pre-P1-10 default.
cfg.SamplingRatio = 0.1
```

当两个字段都未设置时，追踪器提供者会保持历史默认值
`ParentBased(AlwaysSample())`，因此从 v0.2.x 升级的调用方
不会看到行为变化。

## Opt-in semantics

实验性的 `gen_ai.*` semconv 发射受以下门控控制：

```bash
export OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
```

提示词/响应内容捕获默认禁用。若要启用：

```bash
export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true
```

当启用内容捕获时，被捕获的内容在发出前会经过内置的脱敏器（redactor）路由。

## Demo compose flow

演示栈位于 [compose/compose.yaml](./compose/compose.yaml)，使用
`grafana/otel-lgtm` 作为单容器可观测性栈。

运行：

```bash
docker compose -f compose/compose.yaml up --build
```

然后：

1. 在 `http://localhost:3000` 打开 Grafana
2. 确认演示程序发出了一条被包装的链路
3. 在 Tempo 中验证该链路包含 `invoke_agent` → `chat` 的形态

演示程序源码位于 [compose/demo/main.go](./compose/demo/main.go)。

## Cross-repo iteration pattern (INFRA-06)

本仓库是更大的 `llm-agent-ecosystem` 的一部分。本仓库中的本地辅助脚本针对一个常用的 4 仓开发子集，与 [`llm-agent`](https://github.com/costa92/llm-agent)、[`llm-agent-providers`](https://github.com/costa92/llm-agent-providers) 和 [`llm-agent-customer-support`](https://github.com/costa92/llm-agent-customer-support) 并列。对于跨该子集的本地开发：

**针对该子集的推荐做法：** 将全部 4 个仓库克隆为兄弟仓，从其中任一仓运行 `./scripts/workspace.sh`，然后使用 `go.work` 文件进行开发。该工作区文件在每个仓库中都被 `.gitignore` 忽略：

```bash
cd <parent>
git clone https://github.com/costa92/llm-agent.git
git clone https://github.com/costa92/llm-agent-providers.git
git clone https://github.com/costa92/llm-agent-otel.git
git clone https://github.com/costa92/llm-agent-customer-support.git
cd llm-agent-otel
./scripts/workspace.sh    # writes ../go.work pointing at all 4 sibling clones
go build ./...            # now resolves llm-agent against the local sibling
```

**逃生舱（在打标签的发布分支上绝不可用）：** 对于不使用 `go.work` 的一次性迭代，你可以使用 `replace`：

```bash
go mod edit -replace=github.com/costa92/llm-agent=../llm-agent
```

`release-precheck` CI 工作流会拒绝匹配 `release/**` 的分支上任何非空的 `replace` 块。不要从带有 `replace` 指令的分支打标签——INFRA-04。

## Versioning

本仓库通过协调的版本提升波次跟踪 `llm-agent` 核心表面。
查看 `go.mod` 以了解当前精确的兄弟仓锚定；在当前代码
快照中，这包括 `github.com/costa92/llm-agent v0.5.1`、
`github.com/costa92/llm-agent-rag v1.9.0` 和
`github.com/costa92/llm-agent-flow v0.0.7`。

## PR automation

本仓库现在预期 `.github/workflows/pr-governance.yml` 执行一条简单策略：

- 由 `costa92` 撰写的 PR 应自动通过治理，并在必需的检查通过后启用自动合并。
- 同仓的所有者分支应在 PR 被确认合并后由该工作流显式删除。
- 由其他任何人撰写的 PR 应请求 `costa92` 评审，并保持阻塞直到 `costa92` 批准当前 PR head。

该策略设计为配合要求 `go` 和 `governance` 状态检查的分支保护工作，而非 GitHub 内置的必需批准门控。

仓库级的 `deleteBranchOnMerge` 设置仍保持启用作为安全网，但当前经过测试的主路径现在位于 `pr-governance.yml` 自身内部：启用自动合并、等待 PR 可见地被合并、然后用 GitHub API 删除同仓的 head ref。独立的下游清理工作流在推广期间经过测试，已不再是文档记载的主要机制。

完整的多仓治理设计，包括
`llm-agent`、`llm-agent-rag`、`llm-agent-flow`、`llm-agent-providers`、
`llm-agent-otel` 和 `llm-agent-customer-support` 之间的关系，
位于核心仓库文档中：

- [`PR-GOVERNANCE-OVERVIEW.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-OVERVIEW.zh-CN.md)
- [`PR-GOVERNANCE-PROJECTS.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-PROJECTS.zh-CN.md)
- [`PR-GOVERNANCE-RULES.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-RULES.zh-CN.md)
- [`PR-GOVERNANCE-OPERATIONS.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-OPERATIONS.zh-CN.md)

## See also

- [`llm-agent` CLAUDE.md](https://github.com/costa92/llm-agent/blob/main/CLAUDE.zh-CN.md) —— 项目硬规则（仅标准库核心、无 K8s、按 (provider x model) 划分的能力）。
- [`llm-agent` ROADMAP](https://github.com/costa92/llm-agent/blob/main/.planning/ROADMAP.md) —— 8 阶段 v0.3 里程碑计划。
- [`DEPRECATIONS.md`](https://github.com/costa92/llm-agent/blob/main/DEPRECATIONS.zh-CN.md) —— 处于 v0.4 移除轨道上的符号。

## License

MIT —— 见 [LICENSE](LICENSE)。
