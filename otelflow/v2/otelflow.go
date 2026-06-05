package otelflow

import (
	"context"
	"fmt"

	v2flow "github.com/costa92/llm-agent-flow/v2/flow"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/costa92/llm-agent-otel/otelflow/v2"

// flowIdentifier is the optional Engine surface the wrapper probes to
// label spans. *flow.Engine satisfies it; callers supplying a non-Engine
// Runner can pass the id via Config.FlowID.
type flowIdentifier interface {
	FlowID() string
	FlowName() string
}

// baseWrapper decorates a v2 flow.Runner with OTel spans around Run and
// RunStream. It captures the flow id at Wrap time so per-node child spans
// can carry it.
type baseWrapper struct {
	inner    v2flow.Runner
	tracer   trace.Tracer
	flowID   string
	flowName string
}

var _ v2flow.Runner = (*baseWrapper)(nil)

// resumableWrapper additionally exposes the v2 ResumableRunner surface
// (RunResumable/Resume) with suspend/resume spans.
type resumableWrapper struct {
	baseWrapper
	rinner v2flow.ResumableRunner
}

var _ v2flow.ResumableRunner = (*resumableWrapper)(nil)

// Wrap returns a v2 flow.Runner that emits OTel spans around the inner
// runner. When inner also satisfies flow.ResumableRunner, the returned
// value does too (assert via w.(v2flow.ResumableRunner)) — mirroring the
// AppendRunEvents optional-capability precedent.
func Wrap(inner v2flow.Runner, cfg Config) v2flow.Runner {
	tp := cfg.tracerProvider()
	id := cfg.FlowID
	name := ""
	if fi, ok := inner.(flowIdentifier); ok {
		if id == "" {
			id = fi.FlowID()
		}
		name = fi.FlowName()
	}
	base := baseWrapper{
		inner:    inner,
		tracer:   tp.Tracer(instrumentationName),
		flowID:   id,
		flowName: name,
	}
	if ri, ok := inner.(v2flow.ResumableRunner); ok {
		return &resumableWrapper{baseWrapper: base, rinner: ri}
	}
	return &base
}

func (w *baseWrapper) baseAttrs() []attribute.KeyValue {
	out := []attribute.KeyValue{}
	if w.flowID != "" {
		out = append(out, attribute.String(AttrFlowID, w.flowID))
	}
	if w.flowName != "" {
		out = append(out, attribute.String(AttrFlowName, w.flowName))
	}
	return out
}

func (w *baseWrapper) spanName(base string) string {
	if w.flowID != "" {
		return base + " " + w.flowID
	}
	return base
}

// Run is the sync entry. Single root span: flow.run.
func (w *baseWrapper) Run(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	ctx, span := w.tracer.Start(ctx, w.spanName("flow.run"), trace.WithAttributes(w.baseAttrs()...))
	defer span.End()
	span.SetAttributes(attribute.Int(AttrInputCount, len(inputs)))

	out, err := w.inner.Run(ctx, inputs)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return out, err
	}
	span.SetAttributes(attribute.Int(AttrOutputCount, len(out)))
	span.SetStatus(codes.Ok, "")
	return out, nil
}

// RunStream is the streaming entry. One root span "flow.run.stream" plus
// one child span "flow.node" per node, ended on the matching
// NodeFinished. NodeInterrupted/FlowSuspended are annotated as span
// events. The inner channel is drained from a goroutine; the re-emitted
// channel is closed only after the inner one drains.
func (w *baseWrapper) RunStream(ctx context.Context, inputs map[string]any) (<-chan v2flow.Event, error) {
	ctx, root := w.tracer.Start(ctx, w.spanName("flow.run.stream"), trace.WithAttributes(w.baseAttrs()...))
	root.SetAttributes(attribute.Int(AttrInputCount, len(inputs)))

	innerCh, err := w.inner.RunStream(ctx, inputs)
	if err != nil {
		root.RecordError(err)
		root.SetStatus(codes.Error, err.Error())
		root.End()
		return nil, err
	}

	out := make(chan v2flow.Event, 16)
	go func() {
		defer close(out)
		defer root.End()

		nodeSpans := map[string]trace.Span{}
		// Close any node spans still open at stream end, e.g. when ctx
		// is cancelled mid-node.
		defer func() {
			for nodeID, sp := range nodeSpans {
				sp.SetStatus(codes.Error, fmt.Sprintf("node %q span not closed by NodeFinished", nodeID))
				sp.End()
			}
		}()

		var terminalErr error
		for ev := range innerCh {
			w.handleEvent(ctx, ev, nodeSpans, root)
			if ev.Kind == v2flow.FlowErr {
				terminalErr = ev.Err
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				root.RecordError(ctx.Err())
				root.SetStatus(codes.Error, ctx.Err().Error())
				return
			}
		}
		if terminalErr != nil {
			root.RecordError(terminalErr)
			root.SetStatus(codes.Error, terminalErr.Error())
		} else {
			root.SetStatus(codes.Ok, "")
		}
	}()
	return out, nil
}

// handleEvent updates the per-node child-span map in reaction to one v2
// Event. Observability is strictly a side-effect; the event is forwarded
// unmodified.
func (w *baseWrapper) handleEvent(ctx context.Context, ev v2flow.Event, nodeSpans map[string]trace.Span, root trace.Span) {
	switch ev.Kind {
	case v2flow.NodeStarted:
		if ev.NodeID == "" {
			return
		}
		attrs := append([]attribute.KeyValue{
			attribute.String(AttrNodeID, ev.NodeID),
		}, w.baseAttrs()...)
		_, sp := w.tracer.Start(ctx, "flow.node "+ev.NodeID, trace.WithAttributes(attrs...))
		nodeSpans[ev.NodeID] = sp
	case v2flow.NodeFinished:
		sp, ok := nodeSpans[ev.NodeID]
		if !ok {
			return
		}
		delete(nodeSpans, ev.NodeID)
		if ev.Err != nil {
			sp.RecordError(ev.Err)
			sp.SetStatus(codes.Error, ev.Err.Error())
		} else {
			sp.SetStatus(codes.Ok, "")
		}
		sp.End()
	case v2flow.NodeSkipped:
		if ev.NodeID == "" {
			return
		}
		attrs := append([]attribute.KeyValue{
			attribute.String(AttrNodeID, ev.NodeID),
			attribute.Bool(AttrNodeSkipped, true),
		}, w.baseAttrs()...)
		_, sp := w.tracer.Start(ctx, "flow.node "+ev.NodeID, trace.WithAttributes(attrs...))
		sp.End()
	case v2flow.NodeInterrupted:
		// Annotate on the node span if open, else on the root.
		target := root
		if sp, ok := nodeSpans[ev.NodeID]; ok {
			target = sp
		}
		attrs := []attribute.KeyValue{attribute.String(AttrNodeID, ev.NodeID)}
		if ev.Request != nil {
			attrs = append(attrs,
				attribute.String("flow.interrupt.kind", ev.Request.Kind),
				attribute.String("flow.interrupt.prompt", ev.Request.Prompt),
			)
		}
		target.AddEvent("flow.node.interrupted", trace.WithAttributes(attrs...))
	case v2flow.FlowSuspended:
		attrs := []attribute.KeyValue{attribute.String(AttrNodeID, ev.NodeID)}
		if ev.ResumeToken != "" {
			attrs = append(attrs, attribute.String(AttrResumeToken, ev.ResumeToken))
		}
		root.AddEvent("flow.suspended", trace.WithAttributes(attrs...))
	}
}

// RunResumable executes a fresh resumable run. On suspend the result is
// not an error: AttrResumeToken is attached and the span status is Ok.
func (w *resumableWrapper) RunResumable(ctx context.Context, runID string, inputs map[string]any) (v2flow.RunResult, error) {
	ctx, span := w.tracer.Start(ctx, w.spanName("flow.suspend"), trace.WithAttributes(w.baseAttrs()...))
	defer span.End()
	span.SetAttributes(attribute.Int(AttrInputCount, len(inputs)))

	res, err := w.rinner.RunResumable(ctx, runID, inputs)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, err
	}
	if res.Suspended != nil {
		span.SetAttributes(attribute.String(AttrResumeToken, res.Suspended.ResumeToken))
		if res.Suspended.NodeID != "" {
			span.SetAttributes(attribute.String(AttrNodeID, res.Suspended.NodeID))
		}
	} else {
		span.SetAttributes(attribute.Int(AttrOutputCount, len(res.Outputs)))
	}
	span.SetStatus(codes.Ok, "")
	return res, nil
}

// Resume continues a suspended run identified by token. The span carries
// AttrResumeToken.
func (w *resumableWrapper) Resume(ctx context.Context, runID, token string, humanInput map[string]any) (v2flow.RunResult, error) {
	ctx, span := w.tracer.Start(ctx, w.spanName("flow.resume"),
		trace.WithAttributes(append(w.baseAttrs(), attribute.String(AttrResumeToken, token))...))
	defer span.End()

	res, err := w.rinner.Resume(ctx, runID, token, humanInput)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, err
	}
	if res.Suspended != nil {
		span.SetAttributes(attribute.String("flow.next_resume_token", res.Suspended.ResumeToken))
	} else {
		span.SetAttributes(attribute.Int(AttrOutputCount, len(res.Outputs)))
	}
	span.SetStatus(codes.Ok, "")
	return res, nil
}
