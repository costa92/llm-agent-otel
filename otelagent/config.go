package otelagent

import "go.opentelemetry.io/otel/trace"

type Config struct {
	TracerProvider trace.TracerProvider
}

func (c Config) tracerProvider() trace.TracerProvider {
	if c.TracerProvider != nil {
		return c.TracerProvider
	}
	return trace.NewNoopTracerProvider()
}
