package otel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestExporterConfig_DefaultsToOTLPHTTP4318(t *testing.T) {
	cfg := DefaultExporterConfig()
	if cfg.Protocol != ProtocolHTTP {
		t.Fatalf("Protocol = %q, want %q", cfg.Protocol, ProtocolHTTP)
	}
	if cfg.Endpoint != "http://localhost:4318" {
		t.Fatalf("Endpoint = %q, want http://localhost:4318", cfg.Endpoint)
	}
}

func TestExporterConfig_AllowsGRPCOptIn(t *testing.T) {
	cfg := DefaultExporterConfig()
	cfg.Protocol = ProtocolGRPC
	cfg.Endpoint = "localhost:4317"
	if cfg.Protocol != ProtocolGRPC {
		t.Fatalf("Protocol = %q, want %q", cfg.Protocol, ProtocolGRPC)
	}
	if cfg.Endpoint != "localhost:4317" {
		t.Fatalf("Endpoint = %q, want localhost:4317", cfg.Endpoint)
	}
}

func TestNewTracerProvider_AcceptsConfig(t *testing.T) {
	tp, err := NewTracerProvider(context.Background(), ExporterConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "http://localhost:4318",
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("NewTracerProvider(): %v", err)
	}
	if tp == nil {
		t.Fatal("NewTracerProvider() returned nil provider")
	}
}

func TestComposeAssetsExist(t *testing.T) {
	mustExist(t, "compose/compose.yaml")
	mustExist(t, "compose/demo/main.go")
}

func TestREADME_DocumentsOptInAndDemo(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("ReadFile(README.md): %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental",
		"OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT",
		"compose/compose.yaml",
		"grafana/otel-lgtm",
		"otelslog",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("README missing %q", want)
		}
	}
}

func mustExist(t *testing.T, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Clean(rel)); err != nil {
		t.Fatalf("%s missing: %v", rel, err)
	}
}

// --- P1-10 / P1-11 coverage --------------------------------------------------

// clearOTELEnv unsets the standard OTEL_EXPORTER_OTLP_* env vars so each test
// starts from a known baseline regardless of caller environment.
func clearOTELEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_INSECURE",
	} {
		t.Setenv(k, "")
	}
}

// A: caller-provided Sampler is honored end-to-end.
func TestNewTracerProvider_HonorsExplicitSampler(t *testing.T) {
	clearOTELEnv(t)
	tp, err := NewTracerProvider(context.Background(), ExporterConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "http://localhost:4318",
		Insecure: true,
		Sampler:  sdktrace.NeverSample(),
	})
	if err != nil {
		t.Fatalf("NewTracerProvider(): %v", err)
	}
	tr := tp.Tracer("p1-10-test")
	_, span := tr.Start(context.Background(), "should-not-sample")
	defer span.End()
	if span.SpanContext().IsSampled() {
		t.Fatalf("span IsSampled() = true, want false (NeverSample sampler ignored)")
	}
}

// B: SamplingRatio is honored when Sampler is nil.
func TestNewTracerProvider_HonorsSamplingRatio(t *testing.T) {
	clearOTELEnv(t)
	tp, err := NewTracerProvider(context.Background(), ExporterConfig{
		Protocol:      ProtocolHTTP,
		Endpoint:      "http://localhost:4318",
		Insecure:      true,
		SamplingRatio: 0.0001,
	})
	if err != nil {
		t.Fatalf("NewTracerProvider(): %v", err)
	}
	tr := tp.Tracer("p1-10-test")
	sampled := 0
	const n = 1000
	for i := 0; i < n; i++ {
		_, span := tr.Start(context.Background(), "ratio-probe")
		if span.SpanContext().IsSampled() {
			sampled++
		}
		span.End()
	}
	// With ratio 0.0001 and 1000 spans, expect ~0; allow tiny margin but reject
	// "always sample" behavior (which would yield 1000).
	if sampled > 10 {
		t.Fatalf("sampled=%d/1000 spans with ratio 0.0001; SamplingRatio not honored", sampled)
	}
}

// C: explicit Sampler overrides SamplingRatio when both are set.
func TestNewTracerProvider_SamplerOverridesRatio(t *testing.T) {
	clearOTELEnv(t)
	tp, err := NewTracerProvider(context.Background(), ExporterConfig{
		Protocol:      ProtocolHTTP,
		Endpoint:      "http://localhost:4318",
		Insecure:      true,
		Sampler:       sdktrace.NeverSample(),
		SamplingRatio: 1.0,
	})
	if err != nil {
		t.Fatalf("NewTracerProvider(): %v", err)
	}
	tr := tp.Tracer("p1-10-test")
	_, span := tr.Start(context.Background(), "sampler-wins")
	defer span.End()
	if span.SpanContext().IsSampled() {
		t.Fatalf("span IsSampled() = true; explicit Sampler should override SamplingRatio")
	}
}

// D: DefaultExporterConfig honors OTEL_EXPORTER_OTLP_ENDPOINT.
func TestDefaultExporterConfig_HonorsOTELEndpointEnv(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.prod:4318")
	cfg := DefaultExporterConfig()
	if cfg.Endpoint != "http://collector.prod:4318" {
		t.Fatalf("Endpoint = %q, want http://collector.prod:4318", cfg.Endpoint)
	}
}

// E: DefaultExporterConfig honors OTEL_EXPORTER_OTLP_PROTOCOL.
func TestDefaultExporterConfig_HonorsOTELProtocolEnv(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"grpc", ProtocolGRPC},
		{"http/protobuf", ProtocolHTTP},
		{"http", ProtocolHTTP},
		{"", ProtocolHTTP}, // empty → fall back to default
	}
	for _, tc := range cases {
		tc := tc
		t.Run("protocol="+tc.in, func(t *testing.T) {
			clearOTELEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", tc.in)
			cfg := DefaultExporterConfig()
			if cfg.Protocol != tc.want {
				t.Fatalf("Protocol = %q, want %q (env=%q)", cfg.Protocol, tc.want, tc.in)
			}
		})
	}
}

// F: DefaultExporterConfig honors OTEL_EXPORTER_OTLP_INSECURE. Stays clear of
// normalizeExporterConfig — the pre-existing Insecure-fallback bug there is
// out of scope for this PR.
func TestDefaultExporterConfig_HonorsOTELInsecureEnv(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"1", true},
		{"false", false},
		{"0", false},
		{"", true}, // empty → fall back to default (insecure)
	}
	for _, tc := range cases {
		tc := tc
		t.Run("insecure="+tc.in, func(t *testing.T) {
			clearOTELEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tc.in)
			cfg := DefaultExporterConfig()
			if cfg.Insecure != tc.want {
				t.Fatalf("Insecure = %v, want %v (env=%q)", cfg.Insecure, tc.want, tc.in)
			}
		})
	}
}

// G: caller-provided Endpoint must beat env (priority scheme A:
// caller > env > default).
func TestNewTracerProvider_CallerEndpointBeatsEnv(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://env:4318")
	cfg := ExporterConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "http://caller:9999",
		Insecure: true,
	}
	got := normalizeExporterConfig(cfg)
	if got.Endpoint != "http://caller:9999" {
		t.Fatalf("Endpoint = %q, want http://caller:9999 (caller must beat env)", got.Endpoint)
	}
}

// H: when env is empty and caller passed zero values, fall back to hardcoded defaults.
func TestNewTracerProvider_DefaultsWhenNoEnvNoCaller(t *testing.T) {
	clearOTELEnv(t)
	cfg := DefaultExporterConfig()
	if cfg.Endpoint != "http://localhost:4318" {
		t.Fatalf("Endpoint = %q, want http://localhost:4318", cfg.Endpoint)
	}
	if cfg.Protocol != ProtocolHTTP {
		t.Fatalf("Protocol = %q, want %q", cfg.Protocol, ProtocolHTTP)
	}
	if !cfg.Insecure {
		t.Fatalf("Insecure = false, want true")
	}
}

// I: critical for customer-support path — normalize must read env when caller
// leaves Endpoint blank, BEFORE falling back to hardcoded localhost.
func TestNormalizeExporterConfig_FillsEndpointFromEnv(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://x:4318")
	cfg := ExporterConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "",
		Insecure: true,
	}
	got := normalizeExporterConfig(cfg)
	if got.Endpoint != "http://x:4318" {
		t.Fatalf("Endpoint = %q, want http://x:4318 (env should fill caller's blank)", got.Endpoint)
	}
}
