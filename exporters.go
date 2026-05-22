package otel

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/sdk/trace"
)

const (
	ProtocolHTTP = "http"
	ProtocolGRPC = "grpc"
)

// ExporterConfig configures the OTLP span exporter and the tracer-provider
// sampler.
//
// Precedence for Protocol / Endpoint / Insecure when zero-valued at the caller
// is: env (OTEL_EXPORTER_OTLP_{PROTOCOL,ENDPOINT,INSECURE}) > hardcoded
// default. Explicit non-zero caller values always win.
type ExporterConfig struct {
	Protocol string
	Endpoint string
	Insecure bool

	// Sampler, when non-nil, is wired directly into the tracer provider via
	// WithSampler. When nil, SamplingRatio is consulted.
	Sampler trace.Sampler

	// SamplingRatio is consulted only when Sampler is nil. Values in (0, 1]
	// produce ParentBased(TraceIDRatioBased(SamplingRatio)); any other value
	// (including 0 and the zero value) falls back to ParentBased(AlwaysSample()),
	// matching the pre-P1-10 default behavior.
	SamplingRatio float64
}

func DefaultExporterConfig() ExporterConfig {
	cfg := ExporterConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "http://localhost:4318",
		Insecure: true,
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := normalizeProtocol(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")); v != "" {
		cfg.Protocol = v
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Insecure = b
		}
	}
	return cfg
}

func NewTracerProvider(ctx context.Context, cfg ExporterConfig) (*trace.TracerProvider, error) {
	exporter, err := newSpanExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithSampler(resolveSampler(cfg)),
	), nil
}

// resolveSampler picks the sampler for the tracer provider based on cfg.
// Order: cfg.Sampler > TraceIDRatioBased(cfg.SamplingRatio) when ratio is in
// (0, 1] > ParentBased(AlwaysSample()) (equivalent to the SDK default).
func resolveSampler(cfg ExporterConfig) trace.Sampler {
	if cfg.Sampler != nil {
		return cfg.Sampler
	}
	if cfg.SamplingRatio > 0 && cfg.SamplingRatio <= 1 {
		return trace.ParentBased(trace.TraceIDRatioBased(cfg.SamplingRatio))
	}
	return trace.ParentBased(trace.AlwaysSample())
}

func normalizeExporterConfig(cfg ExporterConfig) ExporterConfig {
	// env fills caller-blank fields BEFORE falling back to hardcoded defaults,
	// so callers that construct ExporterConfig directly (e.g. customer-support's
	// app.go) still benefit from OTEL_EXPORTER_OTLP_* env vars.
	if strings.TrimSpace(cfg.Endpoint) == "" {
		if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
			cfg.Endpoint = v
		}
	}
	if strings.TrimSpace(cfg.Protocol) == "" {
		if v := normalizeProtocol(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")); v != "" {
			cfg.Protocol = v
		}
	}

	def := DefaultExporterConfig()
	if strings.TrimSpace(cfg.Protocol) == "" {
		cfg.Protocol = def.Protocol
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = def.Endpoint
	}
	// NOTE: this preserves a pre-existing quirk — an explicit Insecure:false
	// is overwritten by def.Insecure (which itself may be true by default).
	// Fixing that is out of scope for P1-10/11; tracked for a follow-up PR.
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

// normalizeProtocol maps the standard OTEL_EXPORTER_OTLP_PROTOCOL env values
// to this package's protocol constants. An unrecognized or empty input
// returns "" so callers can keep their fallback chain explicit.
func normalizeProtocol(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "grpc":
		return ProtocolGRPC
	case "http", "http/protobuf":
		return ProtocolHTTP
	default:
		return ""
	}
}
