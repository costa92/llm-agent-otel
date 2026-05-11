package otelmetrics

import (
	"context"
	"testing"
	"time"

	otelroot "github.com/costa92/llm-agent-otel"
	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetrics_EmitExpectedInstruments(t *testing.T) {
	rec, reader := newRecorder(t)
	ctx := context.Background()

	rec.RecordTokenUsage(ctx, 12, metricAttrs(
		attribute.String(otelroot.AttrSystem, "openai"),
		attribute.String(otelroot.AttrRequestModel, "gpt-4o-mini"),
		attribute.String(otelroot.AttrOperation, "chat"),
	))
	rec.RecordDuration(ctx, 250*time.Millisecond, metricAttrs(
		attribute.String(otelroot.AttrSystem, "openai"),
		attribute.String(otelroot.AttrRequestModel, "gpt-4o-mini"),
		attribute.String(otelroot.AttrOperation, "chat"),
	))
	rec.RecordTTFT(ctx, 75*time.Millisecond, metricAttrs(
		attribute.String(otelroot.AttrSystem, "openai"),
		attribute.String(otelroot.AttrRequestModel, "gpt-4o-mini"),
		attribute.String(otelroot.AttrOperation, "stream"),
	))

	rm := collectMetrics(t, reader)
	assertMetricNames(t, rm,
		otelroot.MetricClientTokenUsage,
		otelroot.MetricClientOperationDuration,
		otelroot.MetricClientOperationTTFT,
	)
}

func TestCardinality_UserIDsDoNotExplodeMetricAttributes(t *testing.T) {
	rec, reader := newRecorder(t)
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		rec.RecordTokenUsage(ctx, 1, metricAttrs(
			attribute.String(otelroot.AttrSystem, "openai"),
			attribute.String(otelroot.AttrRequestModel, "gpt-4o-mini"),
			attribute.String(otelroot.AttrOperation, "chat"),
			attribute.String(otelroot.AttrUserID, userID(i)),
			attribute.String(otelroot.AttrSessionID, sessionID(i)),
		))
	}

	rm := collectMetrics(t, reader)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got := countAttributeSets(m)
			if got > 50 {
				t.Fatalf("metric %q attribute combinations = %d, want <= 50", m.Name, got)
			}
			assertMetricAttrsDoNotContain(t, m, otelroot.AttrUserID, otelroot.AttrSessionID)
		}
	}
}

func TestContentCapture_DefaultOff(t *testing.T) {
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "")
	attrs := MessageAttributes("hello prompt", "hello answer")
	for _, kv := range attrs {
		if kv.Key == attribute.Key(otelroot.AttrInputMessages) || kv.Key == attribute.Key(otelroot.AttrOutputMessages) {
			t.Fatalf("message content attribute %q present when capture should be off", kv.Key)
		}
	}
}

func TestContentCapture_EnabledAndRedacted(t *testing.T) {
	t.Setenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", "true")
	attrs := MessageAttributes("email test@example.com", "key sk-abcdef")
	if len(attrs) != 2 {
		t.Fatalf("len(attrs) = %d, want 2", len(attrs))
	}
	for _, kv := range attrs {
		if kv.Value.Type().String() != "STRING" {
			t.Fatalf("attr %q value type = %v, want STRING", kv.Key, kv.Value.Type())
		}
		if kv.Value.AsString() == "" {
			t.Fatalf("attr %q empty", kv.Key)
		}
		if kv.Value.AsString() == "email test@example.com" || kv.Value.AsString() == "key sk-abcdef" {
			t.Fatalf("attr %q was not redacted: %q", kv.Key, kv.Value.AsString())
		}
	}
}

func newRecorder(t *testing.T) (*Recorder, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	rec, err := New(Options{MeterProvider: mp})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return rec, reader
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) *metricdata.ResourceMetrics {
	t.Helper()
	rm := &metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect(): %v", err)
	}
	return rm
}

func assertMetricNames(t *testing.T, rm *metricdata.ResourceMetrics, want ...string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			seen[m.Name] = true
		}
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("metric %q missing; saw %v", name, seen)
		}
	}
}

func countAttributeSets(m metricdata.Metrics) int {
	switch data := m.Data.(type) {
	case metricdata.Sum[int64]:
		return len(data.DataPoints)
	case metricdata.Histogram[int64]:
		return len(data.DataPoints)
	case metricdata.Histogram[float64]:
		return len(data.DataPoints)
	default:
		return 0
	}
}

func assertMetricAttrsDoNotContain(t *testing.T, m metricdata.Metrics, forbidden ...string) {
	t.Helper()
	check := func(set attribute.Set) {
		for _, kv := range set.ToSlice() {
			for _, key := range forbidden {
				if string(kv.Key) == key {
					t.Fatalf("metric %q leaked forbidden attr %q", m.Name, key)
				}
			}
		}
	}
	switch data := m.Data.(type) {
	case metricdata.Sum[int64]:
		for _, dp := range data.DataPoints {
			check(dp.Attributes)
		}
	case metricdata.Histogram[int64]:
		for _, dp := range data.DataPoints {
			check(dp.Attributes)
		}
	case metricdata.Histogram[float64]:
		for _, dp := range data.DataPoints {
			check(dp.Attributes)
		}
	}
}

func metricAttrs(kv ...attribute.KeyValue) apimetric.RecordOption {
	return apimetric.WithAttributeSet(attribute.NewSet(kv...))
}

func userID(i int) string    { return "user-" + itoa(i) }
func sessionID(i int) string { return "session-" + itoa(i) }

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	return string(buf[i:])
}
