package otelflow

import "go.opentelemetry.io/otel/trace"

// Config selects the providers used to emit telemetry. A nil
// TracerProvider falls back to the no-op tracer provider — span
// emission silently disables. Mirrors the v0.1 otelflow.Config shape.
type Config struct {
	TracerProvider trace.TracerProvider
	// FlowID, when non-empty, overrides the id pulled from
	// (*flow.Engine).FlowID() for span attributes.
	FlowID string
}

func (c Config) tracerProvider() trace.TracerProvider {
	if c.TracerProvider != nil {
		return c.TracerProvider
	}
	return trace.NewNoopTracerProvider()
}

// Attribute keys local to flow tracing. The flow.* values mirror the v0.1
// otelflow keys where they apply; AttrResumeToken is new for the v2
// resumable surface.
const (
	AttrFlowID        = "flow.id"
	AttrFlowName      = "flow.name"
	AttrNodeID        = "flow.node.id"
	AttrNodeSkipped   = "flow.node.skipped"
	AttrFlowEventKind = "flow.event.kind"
	AttrFinishReason  = "flow.finish_reason"
	AttrOutputCount   = "flow.output_count"
	AttrInputCount    = "flow.input_count"
	AttrResumeToken   = "flow.resume_token"
)
