package otelrag

import (
	"context"
	"time"

	"github.com/costa92/llm-agent-rag/obs"

	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// RAG metric names. Kept local to this package, like the attribute keys —
// they follow the gen_ai.* shape without belonging to that convention space.
const (
	MetricRequests       = "rag.requests"           // RED: request rate
	MetricErrors         = "rag.errors"             // RED: error count
	MetricDuration       = "rag.operation.duration" // RED: operation/stage duration (ms)
	MetricTokens         = "rag.tokens"             // cost: token count
	MetricGenerateTokens = "rag.generate.tokens"    // cost: per-Generate token count (v0.3.0)
)

// RAG metric attribute keys.
const (
	AttrOperation = "rag.operation"
	AttrStage     = "rag.stage"
	AttrTokenKind = "rag.token.kind"
	AttrErrorFlag = "rag.error"
	AttrEstimated = "rag.estimated" // bool: token counts derived from a counter (v0.3.0)
)

// meterProvider returns the configured MeterProvider, or a no-op one.
func (c Config) meterProvider() apimetric.MeterProvider {
	if c.MeterProvider != nil {
		return c.MeterProvider
	}
	return metricnoop.NewMeterProvider()
}

// instruments holds the RAG RED + cost metric instruments. Every field is
// always non-nil: newInstruments substitutes a no-op instrument on a build
// error so Wrap need not return one.
type instruments struct {
	requests       apimetric.Int64Counter
	errors         apimetric.Int64Counter
	duration       apimetric.Float64Histogram
	tokens         apimetric.Int64Counter
	generateTokens apimetric.Int64Counter
}

// newInstruments builds the RAG instruments from meter, falling back to
// no-op instruments for any that fail to build.
func newInstruments(meter apimetric.Meter) instruments {
	noopMeter := metricnoop.NewMeterProvider().Meter(instrumentationName)

	requests, err := meter.Int64Counter(MetricRequests)
	if err != nil {
		requests, _ = noopMeter.Int64Counter(MetricRequests)
	}
	errs, err := meter.Int64Counter(MetricErrors)
	if err != nil {
		errs, _ = noopMeter.Int64Counter(MetricErrors)
	}
	duration, err := meter.Float64Histogram(MetricDuration)
	if err != nil {
		duration, _ = noopMeter.Float64Histogram(MetricDuration)
	}
	tokens, err := meter.Int64Counter(MetricTokens)
	if err != nil {
		tokens, _ = noopMeter.Int64Counter(MetricTokens)
	}
	generateTokens, err := meter.Int64Counter(
		MetricGenerateTokens,
		apimetric.WithDescription("Per-Generate token consumption inside the RAG pipeline, split by stage and kind."),
		apimetric.WithUnit("{token}"),
	)
	if err != nil {
		generateTokens, _ = noopMeter.Int64Counter(MetricGenerateTokens)
	}
	return instruments{
		requests:       requests,
		errors:         errs,
		duration:       duration,
		tokens:         tokens,
		generateTokens: generateTokens,
	}
}

// recordOp emits the RED metrics for one operation: a request count, an
// error count on failure, the operation-level wall-clock duration, and a
// per-stage duration for each obs.Metrics stage.
func (in instruments) recordOp(ctx context.Context, op string, elapsed time.Duration, stages []obs.StageTiming, err error) {
	opAttr := attribute.String(AttrOperation, op)
	in.requests.Add(ctx, 1, apimetric.WithAttributes(opAttr))

	failed := err != nil
	if failed {
		in.errors.Add(ctx, 1, apimetric.WithAttributes(opAttr))
	}
	in.duration.Record(ctx, millis(elapsed), apimetric.WithAttributes(
		opAttr, attribute.Bool(AttrErrorFlag, failed),
	))
	for _, st := range stages {
		in.duration.Record(ctx, millis(st.Duration), apimetric.WithAttributes(
			opAttr, attribute.String(AttrStage, st.Stage),
		))
	}
}

// recordTokens emits the token-cost metric for one operation, split into
// prompt and completion kinds. Zero counts are skipped.
func (in instruments) recordTokens(ctx context.Context, op string, t obs.TokenUsage) {
	opAttr := attribute.String(AttrOperation, op)
	if t.PromptTokens > 0 {
		in.tokens.Add(ctx, int64(t.PromptTokens), apimetric.WithAttributes(
			opAttr, attribute.String(AttrTokenKind, "prompt"),
		))
	}
	if t.CompletionTokens > 0 {
		in.tokens.Add(ctx, int64(t.CompletionTokens), apimetric.WithAttributes(
			opAttr, attribute.String(AttrTokenKind, "completion"),
		))
	}
}

// recordStageTokens emits the per-Generate token-cost metric for one named
// pipeline stage, split into prompt and completion kinds. Zero counts are
// skipped. Safe to call concurrently — synchronous OTel SDK instruments are
// thread-safe. This is the receiver for rag.Observer.OnGenerateUsage
// introduced in llm-agent-rag v1.5.0.
//
// rag.generate.tokens is intentionally separate from rag.tokens: the former
// answers "what did each pipeline stage cost?", the latter answers "what
// did the answer leg cost?". Combining both in one dashboard would
// double-count the answer leg.
func (in instruments) recordStageTokens(ctx context.Context, stage string, usage obs.TokenUsage) {
	if in.generateTokens == nil {
		return
	}
	base := []attribute.KeyValue{
		attribute.String(AttrStage, stage),
		attribute.Bool(AttrEstimated, usage.Estimated),
	}
	if usage.PromptTokens > 0 {
		attrs := append(append([]attribute.KeyValue{}, base...), attribute.String(AttrTokenKind, "prompt"))
		in.generateTokens.Add(ctx, int64(usage.PromptTokens), apimetric.WithAttributes(attrs...))
	}
	if usage.CompletionTokens > 0 {
		attrs := append(append([]attribute.KeyValue{}, base...), attribute.String(AttrTokenKind, "completion"))
		in.generateTokens.Add(ctx, int64(usage.CompletionTokens), apimetric.WithAttributes(attrs...))
	}
}

// millis converts a duration to fractional milliseconds for the histogram.
func millis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
