package otel

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/sdk/trace"
)

const (
	ProtocolHTTP = "http"
	ProtocolGRPC = "grpc"
)

type ExporterConfig struct {
	Protocol string
	Endpoint string
	Insecure bool
}

func DefaultExporterConfig() ExporterConfig {
	return ExporterConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "http://localhost:4318",
		Insecure: true,
	}
}

func NewTracerProvider(ctx context.Context, cfg ExporterConfig) (*trace.TracerProvider, error) {
	exporter, err := newSpanExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return trace.NewTracerProvider(trace.WithBatcher(exporter)), nil
}

func normalizeExporterConfig(cfg ExporterConfig) ExporterConfig {
	def := DefaultExporterConfig()
	if strings.TrimSpace(cfg.Protocol) == "" {
		cfg.Protocol = def.Protocol
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = def.Endpoint
	}
	if !cfg.Insecure {
		cfg.Insecure = def.Insecure
	}
	return cfg
}

func newSpanExporter(ctx context.Context, cfg ExporterConfig) (trace.SpanExporter, error) {
	cfg = normalizeExporterConfig(cfg)
	switch cfg.Protocol {
	case ProtocolHTTP:
		return newHTTPExporter(ctx, cfg)
	case ProtocolGRPC:
		return newGRPCExporter(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported exporter protocol %q", cfg.Protocol)
	}
}
