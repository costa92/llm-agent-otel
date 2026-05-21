package otelflow

import "go.opentelemetry.io/otel/trace"

// Config selects the providers used to emit telemetry. A nil
// TracerProvider falls back to the no-op tracer provider — span
// emission silently disables.
type Config struct {
	TracerProvider trace.TracerProvider
	// FlowID, when non-empty, overrides the id pulled from
	// (*flow.Engine).FlowID() for span attributes. Useful when the
	// caller has its own canonical identifier (e.g. a UUID assigned
	// by an external orchestrator) that should appear in traces
	// instead of the flow.json id.
	FlowID string
}

func (c Config) tracerProvider() trace.TracerProvider {
	if c.TracerProvider != nil {
		return c.TracerProvider
	}
	return trace.NewNoopTracerProvider()
}

// Attribute keys local to flow tracing. The flow.* namespace mirrors
// rag.* in otelrag — not gen_ai semconv.
const (
	AttrFlowID         = "flow.id"
	AttrFlowName       = "flow.name"
	AttrNodeID         = "flow.node.id"
	AttrNodeSkipped    = "flow.node.skipped"
	AttrFlowEventKind  = "flow.event.kind"
	AttrFinishReason   = "flow.finish_reason"
	AttrOutputCount    = "flow.output_count"
	AttrInputCount     = "flow.input_count"
)
