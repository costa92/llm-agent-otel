// Package otelrag wraps github.com/costa92/llm-agent-rag/rag.System to
// emit OpenTelemetry spans around Import, Retrieve, and Ask. It mirrors
// the wrapping pattern used by otelmodel / otelagent in this repo —
// each top-level rag operation creates a span, records rag-specific
// attributes from the returned trace, and ends the span (recording
// errors on failure).
//
// The wrapper does not modify the underlying *rag.System; it composes
// over it so callers can choose to wrap or pass-through per call site.
package otelrag

import (
	"context"

	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
	ragretrieve "github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/costa92/llm-agent-otel/otelrag"

// RAG attribute keys. Kept local to this package — they do not belong
// to the gen_ai.* semantic convention space but follow the same shape.
const (
	AttrNamespace          = "rag.namespace"
	AttrTopK               = "rag.top_k"
	AttrHitCount           = "rag.hit_count"
	AttrImportDocs         = "rag.import.documents"
	AttrImportChunks       = "rag.import.chunks"
	AttrImportEmbedCount   = "rag.import.embed_count"
	AttrImportRemoved      = "rag.import.removed_chunks"
	AttrRouteMode          = "rag.route.mode"
	AttrRouteGap           = "rag.route.gap"
	AttrRouteSelectedCount = "rag.route.selected_count"
	AttrRouteCandidates    = "rag.route.candidate_count"
)

// Operation names for span identification.
const (
	OperationImport   = "rag.import"
	OperationRetrieve = "rag.retrieve"
	OperationAsk      = "rag.ask"
)

// Config selects the TracerProvider used to emit spans. Nil falls back
// to the no-op tracer provider.
type Config struct {
	TracerProvider trace.TracerProvider
}

func (c Config) tracerProvider() trace.TracerProvider {
	if c.TracerProvider != nil {
		return c.TracerProvider
	}
	return trace.NewNoopTracerProvider()
}

// Wrapper composes over a *rag.System and emits a span per top-level
// operation. The wrapper exposes parallel methods rather than satisfying
// the System type (which is a concrete struct, not an interface).
type Wrapper struct {
	inner  *rag.System
	tracer trace.Tracer
}

// Wrap returns a Wrapper around sys. Pass an optional Config to select a
// non-default tracer provider.
func Wrap(sys *rag.System, opts ...Config) *Wrapper {
	cfg := Config{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	tp := cfg.tracerProvider()
	return &Wrapper{
		inner:  sys,
		tracer: tp.Tracer(instrumentationName),
	}
}

// Inner returns the wrapped *rag.System. Useful when callers need to
// reach through the wrapper for an unwrapped operation (e.g. tests).
func (w *Wrapper) Inner() *rag.System { return w.inner }

// Import runs the underlying Import inside an "rag.import" span.
func (w *Wrapper) Import(ctx context.Context, docs []ingest.Document, opts ingest.ImportOptions) (ingest.ImportResult, error) {
	ctx, span := w.tracer.Start(ctx, OperationImport)
	defer span.End()
	if opts.Namespace != "" {
		span.SetAttributes(attribute.String(AttrNamespace, opts.Namespace))
	}
	res, err := w.inner.Import(ctx, docs, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, err
	}
	span.SetAttributes(
		attribute.Int(AttrImportDocs, res.Documents),
		attribute.Int(AttrImportChunks, res.Chunks),
	)
	return res, nil
}

// Retrieve runs the underlying Retrieve inside an "rag.retrieve" span.
// Route-policy attributes are populated from the retrieve.Trace via the
// inner retrieve path (we re-run the internal retrieve helper-shaped
// path here to keep the trace accessible).
func (w *Wrapper) Retrieve(ctx context.Context, query string, opts rag.SearchOptions) ([]store.Hit, error) {
	ctx, span := w.tracer.Start(ctx, OperationRetrieve)
	defer span.End()
	span.SetAttributes(
		attribute.String(AttrNamespace, opts.Namespace),
		attribute.Int(AttrTopK, opts.TopK),
	)
	hits, err := w.inner.Retrieve(ctx, query, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Int(AttrHitCount, len(hits)))
	return hits, nil
}

// Ask runs the underlying Ask inside an "rag.ask" span and records
// rag.Trace attributes (route mode, gap, candidate counts, etc.).
func (w *Wrapper) Ask(ctx context.Context, question string, opts rag.AskOptions) (rag.Answer, error) {
	ctx, span := w.tracer.Start(ctx, OperationAsk)
	defer span.End()
	span.SetAttributes(
		attribute.String(AttrNamespace, opts.Search.Namespace),
		attribute.Int(AttrTopK, opts.Search.TopK),
	)
	ans, err := w.inner.Ask(ctx, question, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return ans, err
	}
	span.SetAttributes(
		attribute.Int(AttrHitCount, ans.Diagnostics.HitCount),
	)
	if rp := ans.Trace.RoutePolicy; rp.Mode != "" {
		span.SetAttributes(
			attribute.String(AttrRouteMode, rp.Mode),
			attribute.Int(AttrRouteSelectedCount, rp.SelectedCount),
			attribute.Int(AttrRouteCandidates, rp.CandidateCount),
		)
		if rp.Gap != 0 {
			span.SetAttributes(attribute.Float64(AttrRouteGap, rp.Gap))
		}
	}
	return ans, nil
}

// Observer returns a rag.Observer that emits span events on the
// currently-active span (if any). It's complementary to Wrap: callers
// who don't want to wrap the System can attach this observer instead
// and get event-level attribution on whatever span their own code
// created. Spans from this observer have no duration of their own —
// they are AddEvent calls on the active context's span.
func Observer(cfg ...Config) rag.Observer {
	c := Config{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	tp := c.tracerProvider()
	tracer := tp.Tracer(instrumentationName)
	_ = tracer // reserved for future per-event tracer use
	return rag.Observer{
		OnImport: func(ctx context.Context, t rag.ImportTrace) {
			span := trace.SpanFromContext(ctx)
			span.AddEvent(OperationImport,
				trace.WithAttributes(
					attribute.String(AttrNamespace, t.Namespace),
					attribute.Int(AttrImportDocs, t.Documents),
					attribute.Int(AttrImportChunks, t.Chunks),
					attribute.Int(AttrImportEmbedCount, t.EmbedCount),
					attribute.Int(AttrImportRemoved, t.RemovedChunks),
				))
		},
		OnRetrieve: func(ctx context.Context, rt ragretrieve.Trace) {
			span := trace.SpanFromContext(ctx)
			attrs := []attribute.KeyValue{}
			if rp := rt.RoutePolicy; rp.Mode != "" {
				attrs = append(attrs,
					attribute.String(AttrRouteMode, rp.Mode),
					attribute.Int(AttrRouteSelectedCount, rp.SelectedCount),
					attribute.Int(AttrRouteCandidates, rp.CandidateCount),
				)
			}
			span.AddEvent(OperationRetrieve, trace.WithAttributes(attrs...))
		},
		OnAsk: func(ctx context.Context, t rag.Trace) {
			span := trace.SpanFromContext(ctx)
			attrs := []attribute.KeyValue{
				attribute.String(AttrNamespace, t.Namespace),
				attribute.Int(AttrTopK, t.TopK),
			}
			if rp := t.RoutePolicy; rp.Mode != "" {
				attrs = append(attrs,
					attribute.String(AttrRouteMode, rp.Mode),
					attribute.Int(AttrRouteSelectedCount, rp.SelectedCount),
					attribute.Int(AttrRouteCandidates, rp.CandidateCount),
				)
			}
			span.AddEvent(OperationAsk, trace.WithAttributes(attrs...))
		},
	}
}
