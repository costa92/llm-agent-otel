package otel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
