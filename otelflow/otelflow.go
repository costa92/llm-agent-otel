package otelflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/costa92/llm-agent-flow/flow"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/costa92/llm-agent-otel/otelflow"

// wrapper is the decorating Runner. It captures the flow id at Wrap
// time so RunStream can attach it to every per-node child span.
type wrapper struct {
	inner    flow.Runner
	tracer   trace.Tracer
	flowID   string // overridden by Config.FlowID when set
	flowName string
}

// flowIdentifier is the (subset of) Engine surface the wrapper needs.
// Engine satisfies it; users who supply a non-Engine Runner can pass
// the id explicitly via Config.FlowID.
type flowIdentifier interface {
	FlowID() string
	FlowName() string
}

// Wrap returns a flow.Runner that emits OTel spans around the inner
// runner's Run and RunStream calls. Spans inherit the caller's
// context so traces compose with any parent (HTTP request, queue
// consumer, etc).
func Wrap(inner flow.Runner, cfg Config) flow.Runner {
	tp := cfg.tracerProvider()
	id := cfg.FlowID
	name := ""
	if id == "" {
		if fi, ok := inner.(flowIdentifier); ok {
			id = fi.FlowID()
			name = fi.FlowName()
		}
	} else if fi, ok := inner.(flowIdentifier); ok {
		name = fi.FlowName()
	}
	return &wrapper{
		inner:    inner,
		tracer:   tp.Tracer(instrumentationName),
		flowID:   id,
		flowName: name,
	}
}

func (w *wrapper) baseAttrs() []attribute.KeyValue {
	out := []attribute.KeyValue{}
	if w.flowID != "" {
		out = append(out, attribute.String(AttrFlowID, w.flowID))
	}
	if w.flowName != "" {
		out = append(out, attribute.String(AttrFlowName, w.flowName))
	}
	return out
}

// Run is the sync entry. Single root span: flow.run.
func (w *wrapper) Run(ctx context.Context, inputs map[string]string) (map[string]string, error) {
	name := "flow.run"
	if w.flowID != "" {
		name = "flow.run " + w.flowID
	}
	ctx, span := w.tracer.Start(ctx, name, trace.WithAttributes(w.baseAttrs()...))
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

// RunStream is the streaming entry. Two layers of spans:
//
//   - One root span "flow.run.stream" for the whole run.
//   - One child span "flow.node" per NodeStarted / NodeSkipped event,
//     ended on the matching NodeFinished (NodeSkipped is closed
//     immediately since there's no follow-up).
//
// The wrapper consumes the inner channel from a goroutine to
// preserve the streaming contract; the outer channel is closed only
// after the inner one drains.
func (w *wrapper) RunStream(ctx context.Context, inputs map[string]string) (<-chan flow.FlowEvent, error) {
	rootName := "flow.run.stream"
	if w.flowID != "" {
		rootName = "flow.run.stream " + w.flowID
	}
	ctx, root := w.tracer.Start(ctx, rootName, trace.WithAttributes(w.baseAttrs()...))
	root.SetAttributes(attribute.Int(AttrInputCount, len(inputs)))

	innerCh, err := w.inner.RunStream(ctx, inputs)
	if err != nil {
		root.RecordError(err)
		root.SetStatus(codes.Error, err.Error())
		root.End()
		return nil, err
	}

	out := make(chan flow.FlowEvent, 16)
	go func() {
		defer close(out)
		defer root.End()

		nodeSpans := map[string]trace.Span{}
		// Ensure any node spans still open at stream end are closed,
		// e.g. when ctx is cancelled mid-node.
		defer func() {
			for nodeID, sp := range nodeSpans {
				sp.SetStatus(codes.Error, fmt.Sprintf("node %q span not closed by NodeFinished", nodeID))
				sp.End()
			}
		}()

		var terminalErr error
		for ev := range innerCh {
			w.handleEvent(ctx, ev, nodeSpans)
			if ev.Kind == flow.FlowErr {
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

// handleEvent updates the per-node child-span map in reaction to one
// FlowEvent. The wrapper does not modify the event before forwarding;
// observability is strictly side-effect.
func (w *wrapper) handleEvent(ctx context.Context, ev flow.FlowEvent, nodeSpans map[string]trace.Span) {
	switch ev.Kind {
	case flow.NodeStarted:
		if ev.NodeID == "" {
			return
		}
		attrs := append([]attribute.KeyValue{
			attribute.String(AttrNodeID, ev.NodeID),
		}, w.baseAttrs()...)
		_, sp := w.tracer.Start(ctx, "flow.node "+ev.NodeID, trace.WithAttributes(attrs...))
		nodeSpans[ev.NodeID] = sp
	case flow.NodeFinished:
		sp, ok := nodeSpans[ev.NodeID]
		if !ok {
			// Unbalanced — record on the root via implicit current
			// span if available, but mostly: just skip cleanly.
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
	case flow.NodeSkipped:
		if ev.NodeID == "" {
			return
		}
		attrs := append([]attribute.KeyValue{
			attribute.String(AttrNodeID, ev.NodeID),
			attribute.Bool(AttrNodeSkipped, true),
		}, w.baseAttrs()...)
		_, sp := w.tracer.Start(ctx, "flow.node "+ev.NodeID, trace.WithAttributes(attrs...))
		sp.End()
	case flow.FlowDone:
		// Root span attrs are set on stream-end after the loop.
		_ = errors.New // silence import lints if any drop
	}
}
