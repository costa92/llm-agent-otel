package otelslog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	otelroot "github.com/costa92/llm-agent-otel"
	"go.opentelemetry.io/otel/trace"
)

func TestHandler_AddsTraceAndSpanIDsFromContext(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(NewHandler(base, Options{}))

	ctx := contextWithSpanContext()
	logger.InfoContext(ctx, "hello")

	rec := decodeJSONLog(t, buf.Bytes())
	if rec["trace_id"] == "" {
		t.Fatal("trace_id missing")
	}
	if rec["span_id"] == "" {
		t.Fatal("span_id missing")
	}
}

func TestHandler_PreservesGenAIFields(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(NewHandler(base, Options{}))

	logger.Info("chat complete",
		slog.String(otelroot.AttrSystem, "openai"),
		slog.String(otelroot.AttrRequestModel, "gpt-4o-mini"),
		slog.Int(otelroot.AttrUsageInput, 12),
	)

	rec := decodeJSONLog(t, buf.Bytes())
	if got := rec[otelroot.AttrSystem]; got != "openai" {
		t.Fatalf("%s = %v, want openai", otelroot.AttrSystem, got)
	}
	if got := rec[otelroot.AttrRequestModel]; got != "gpt-4o-mini" {
		t.Fatalf("%s = %v, want gpt-4o-mini", otelroot.AttrRequestModel, got)
	}
	if got := rec[otelroot.AttrUsageInput]; got != float64(12) {
		t.Fatalf("%s = %v, want 12", otelroot.AttrUsageInput, got)
	}
}

func TestHandler_WithAttrsAndGroupsCompose(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(NewHandler(base, Options{})).
		With("component", "bridge").
		WithGroup("nested")

	logger.Info("msg", slog.String("field", "value"))

	raw := string(buf.Bytes())
	if !strings.Contains(raw, `"component":"bridge"`) {
		t.Fatalf("component attr missing from %s", raw)
	}
	if !strings.Contains(raw, `"nested":{"field":"value"}`) {
		t.Fatalf("grouped attr missing from %s", raw)
	}
}

func decodeJSONLog(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b), &out); err != nil {
		t.Fatalf("json.Unmarshal(): %v; raw=%s", err, string(b))
	}
	return out
}

func contextWithSpanContext() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		SpanID:     trace.SpanID{2, 2, 2, 2, 2, 2, 2, 2},
		TraceFlags: trace.FlagsSampled,
		Remote:     false,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}
