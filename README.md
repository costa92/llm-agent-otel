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
go get github.com/costa92/llm-agent-otel@v0.1.0   # available after Phase 5
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

This repo lives in a 4-repo umbrella alongside [`llm-agent`](https://github.com/costa92/llm-agent), [`llm-agent-providers`](https://github.com/costa92/llm-agent-providers), and [`llm-agent-customer-support`](https://github.com/costa92/llm-agent-customer-support). For local development across repos:

**Recommended:** clone all 4 repos as siblings, run `./scripts/workspace.sh` from any of them, then develop with a `go.work` file. The workspace file is `.gitignore`d in every repo:

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

This repo tracks `v0.1.x` for the `llm-agent v0.3.x` cycle. Sister-repo bumps coordinate with core breaking changes; coordinated tags (Phase 7) advance both repos in lockstep.

## See also

- [`llm-agent` CLAUDE.md](https://github.com/costa92/llm-agent/blob/main/CLAUDE.md) — project hard rules (stdlib-only core, no K8s, capability per-(provider x model)).
- [`llm-agent` ROADMAP](https://github.com/costa92/llm-agent/blob/main/.planning/ROADMAP.md) — 8-phase v0.3 milestone plan.
- [`DEPRECATIONS.md`](https://github.com/costa92/llm-agent/blob/main/DEPRECATIONS.md) — symbols on the v0.4 removal track.

## License

MIT — see [LICENSE](LICENSE).
