// Package otelflow provides an OpenTelemetry decorator over the v2 flow
// Runner (github.com/costa92/llm-agent-flow/v2/flow).
//
// It mirrors the v0.1 otelflow package but targets the v2 typed-union
// Event surface and any-keyed port maps. Unlike v0.1, the v2 flow Runner
// may also satisfy flow.ResumableRunner (checkpoint/interrupt/resume); when
// the wrapped inner runner does, Wrap returns a value that ALSO satisfies
// flow.ResumableRunner, exposing RunResumable/Resume with their own spans.
//
// otel dependencies intentionally live here (this module already requires
// otel v1.43.0) rather than in the v2 flow go.mod, keeping the engine free
// of observability deps.
package otelflow
