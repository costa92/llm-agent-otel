# llm-agent-otel

OpenTelemetry decorator wrappers for [`github.com/costa92/llm-agent`](https://github.com/costa92/llm-agent). Wraps `llm.ChatModel` (and the `agents.Agent` interface) with `gen_ai.*` semconv-aware spans, metrics, and slog bridge — without touching the core repo's stdlib-only invariant.

This repo now ships the Phase 5 observability surface:

- `otelmodel.Wrap(...)` for `llm.ChatModel`
- `otelagent.Wrap(...)` for `agents.Agent`
- `otelslog.NewHandler(...)` for `slog.Handler`
- centralized `gen_ai.*` constants and gates
- low-cardinality metrics helpers in `otelmetrics/`
- OTLP exporter wiring defaults
- a `compose/compose.yaml` demo using `grafana/otel-lgtm`

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

`otelmodel.Wrap(...)` preserves `ToolCaller`, `Embedder`, and
`StructuredOutputs` when the inner model implements them.

## RAG per-stage token metrics (`otelrag` v0.3.0)

`otelrag` exposes two wiring paths for the
`rag.Observer.OnGenerateUsage` hook shipped in `llm-agent-rag` v1.5.0.
Both record to a new instrument:

| Metric                  | Kind          | Attributes                                                    |
| ----------------------- | ------------- | ------------------------------------------------------------- |
| `rag.generate.tokens`   | Int64Counter  | `rag.stage` (e.g. `ask`, `reflection_decision`, `grader`, `planner`), `rag.token.kind` (`prompt`/`completion`), `rag.estimated` (bool) |

**Path A — `Observer(cfg)` factory** (covers all four hooks at once):

```go
mp := otel.GetMeterProvider()
observer := otelrag.Observer(otelrag.Config{MeterProvider: mp})
sys := rag.New(rag.Options{Observer: observer /* ... */})
wrapper := otelrag.Wrap(sys, otelrag.Config{MeterProvider: mp})
```

**Path B — `MakeOnGenerateUsageHook` escape hatch** (compose with your own hooks):

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

`otelrag.Wrap(sys, cfg)` does **not** auto-install `OnGenerateUsage` —
`*rag.System` does not expose its Observer for post-construction mutation,
so the hook must be wired before `rag.New(opts)`.

`rag.generate.tokens` is independent from the existing `rag.tokens` counter
and from any token attribution emitted by `otelmodel` on the underlying
`llm.ChatModel`. Combining `rag.generate.tokens` and `rag.tokens` in a single
query would double-count the answer leg — filter by attribute set instead.
Only `prompt` and `completion` kinds are emitted (no `total`) for the same
reason.

## Exporter defaults

`DefaultExporterConfig()` currently defaults to OTLP HTTP on `http://localhost:4318`:

```go
cfg := otel.DefaultExporterConfig()
// Protocol: "http"
// Endpoint: "http://localhost:4318"
```

To opt into OTLP gRPC instead:

```go
cfg := otel.DefaultExporterConfig()
cfg.Protocol = otel.ProtocolGRPC
cfg.Endpoint = "localhost:4317"
```

## Standard OTel env vars

`DefaultExporterConfig()` and `NewTracerProvider(ctx, cfg)` both honor the
standard OpenTelemetry exporter env vars:

| Env var                              | Effect                                         |
| ------------------------------------ | ---------------------------------------------- |
| `OTEL_EXPORTER_OTLP_ENDPOINT`        | Overrides `Endpoint`                           |
| `OTEL_EXPORTER_OTLP_PROTOCOL`        | `grpc` → `ProtocolGRPC`; `http` / `http/protobuf` → `ProtocolHTTP` |
| `OTEL_EXPORTER_OTLP_INSECURE`        | Parsed as bool — overrides `Insecure`          |

Precedence is **caller > env > hardcoded default**: any non-zero field on the
`ExporterConfig` value you hand to `NewTracerProvider` is preserved verbatim;
the env vars only fill caller-blank fields; the hardcoded defaults
(`http://localhost:4318`, `ProtocolHTTP`, `Insecure: true`) apply only when
both caller and env are silent.

```bash
# Switch the whole process to a remote OTLP collector without code changes
export OTEL_EXPORTER_OTLP_ENDPOINT=http://collector.prod:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_INSECURE=false
```

## Sampling

`ExporterConfig` exposes two sampler controls:

```go
cfg := otel.DefaultExporterConfig()

// Option 1: hand the SDK a fully-built Sampler. Wins over SamplingRatio.
cfg.Sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))

// Option 2: ratio-only shortcut. Values in (0, 1] become
// ParentBased(TraceIDRatioBased(ratio)); any other value falls back to
// ParentBased(AlwaysSample()), matching the pre-P1-10 default.
cfg.SamplingRatio = 0.1
```

When neither field is set, the tracer provider keeps the historical default of
`ParentBased(AlwaysSample())`, so callers upgrading from v0.2.x see no
behavior change.

## Opt-in semantics

Experimental `gen_ai.*` semconv emission is gated behind:

```bash
export OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
```

Prompt/response content capture is disabled by default. To enable it:

```bash
export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true
```

When content capture is enabled, captured content is routed through the built-in
redactor before being emitted.

## Demo compose flow

The demo stack lives at [compose/compose.yaml](./compose/compose.yaml) and uses
`grafana/otel-lgtm` as the one-container observability stack.

Run:

```bash
docker compose -f compose/compose.yaml up --build
```

Then:

1. Open Grafana at `http://localhost:3000`
2. Confirm the demo program emits a wrapped trace
3. Verify the trace contains the `invoke_agent` → `chat` shape in Tempo

The demo program source is at [compose/demo/main.go](./compose/demo/main.go).

## Cross-repo iteration pattern (INFRA-06)

This repo is part of the broader `llm-agent-ecosystem`. The local helper script in this repo targets a common 4-repo development subset alongside [`llm-agent`](https://github.com/costa92/llm-agent), [`llm-agent-providers`](https://github.com/costa92/llm-agent-providers), and [`llm-agent-customer-support`](https://github.com/costa92/llm-agent-customer-support). For local development across that subset:

**Recommended for this subset:** clone all 4 repos as siblings, run `./scripts/workspace.sh` from any of them, then develop with a `go.work` file. The workspace file is `.gitignore`d in every repo:

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

**Escape hatch (NEVER on tagged-release branches):** for one-off iteration without `go.work`, you can use `replace`:

```bash
go mod edit -replace=github.com/costa92/llm-agent=../llm-agent
```

The `release-precheck` CI workflow rejects any non-empty `replace` block on branches matching `release/**`. Don't tag from a branch with `replace` directives — INFRA-04.

## Versioning

This repo tracks the `llm-agent` core surface through coordinated bump waves.
Check `go.mod` for the current exact sibling pins; in the current code
snapshot that includes `github.com/costa92/llm-agent v0.5.1`,
`github.com/costa92/llm-agent-rag v1.9.0`, and
`github.com/costa92/llm-agent-flow v0.0.7`.

## PR automation

This repo now expects `.github/workflows/pr-governance.yml` to enforce a simple policy:

- PRs authored by `costa92` should pass governance automatically and enable auto-merge after required checks pass.
- Same-repo owner branches should be deleted explicitly by that workflow after the PR is confirmed merged.
- PRs authored by anyone else should request review from `costa92` and stay blocked until `costa92` approves the current PR head.

This policy is designed to work with branch protection that requires the `go` and `governance` status checks, instead of GitHub's built-in required-approval gate.

The repo-level `deleteBranchOnMerge` setting remains enabled as a safety net, but the primary tested path is now inside `pr-governance.yml` itself: enable auto-merge, wait until the PR is visibly merged, then delete the same-repo head ref with the GitHub API. Standalone downstream cleanup workflows were tested during rollout and are no longer the documented primary mechanism.

The full multi-repo governance design, including the relationship between
`llm-agent`, `llm-agent-rag`, `llm-agent-flow`, `llm-agent-providers`,
`llm-agent-otel`, and `llm-agent-customer-support`, lives in the core repo docs:

- [`PR-GOVERNANCE-OVERVIEW.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-OVERVIEW.md)
- [`PR-GOVERNANCE-PROJECTS.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-PROJECTS.md)
- [`PR-GOVERNANCE-RULES.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-RULES.md)
- [`PR-GOVERNANCE-OPERATIONS.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-OPERATIONS.md)

## See also

- [`llm-agent` CLAUDE.md](https://github.com/costa92/llm-agent/blob/main/CLAUDE.md) — project hard rules (stdlib-only core, no K8s, capability per-(provider x model)).
- [`llm-agent` ROADMAP](https://github.com/costa92/llm-agent/blob/main/.planning/ROADMAP.md) — 8-phase v0.3 milestone plan.
- [`DEPRECATIONS.md`](https://github.com/costa92/llm-agent/blob/main/DEPRECATIONS.md) — symbols on the v0.4 removal track.

## License

MIT — see [LICENSE](LICENSE).
