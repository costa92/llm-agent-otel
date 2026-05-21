// Package otelflow wraps a github.com/costa92/llm-agent-flow/flow.Runner to
// emit OpenTelemetry spans around Run and RunStream. It mirrors the
// wrapping pattern used by otelmodel / otelrag / otelagent in this
// repo — composes over the inner Runner so callers choose whether to
// wrap per call site.
//
// One root span per Run / RunStream:
//
//	flow.run         (id = <FlowID>)
//
// Within a RunStream call, one child span is created per node:
//
//	flow.node        (node_id = <id>; skipped = true|false)
//
// Skipped nodes (CEL guard evaluated false) produce a zero-duration
// child span with the `flow.node.skipped` attribute set to true so
// the trace makes the topology explicit instead of silently omitting
// the node.
//
// FlowEvent stream forwarding is non-blocking: the wrapper consumes
// the inner channel in a goroutine, mirrors events to the outer
// channel and closes it; the root span ends after the inner channel
// drains (including on context cancellation).
//
// Usage:
//
//	import (
//	    "github.com/costa92/llm-agent-flow/flow"
//	    "github.com/costa92/llm-agent-otel/otelflow"
//	    "go.opentelemetry.io/otel"
//	)
//
//	eng, _ := flow.LoadCompile(r, reg, deps)
//	runner := otelflow.Wrap(eng, otelflow.Config{
//	    TracerProvider: otel.GetTracerProvider(),
//	})
//	out, err := runner.Run(ctx, inputs)
package otelflow
