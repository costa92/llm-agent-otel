package otelrag_test

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-otel/otelrag"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/rag"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	if len(req.Messages) > 0 {
		return generate.Response{Text: req.Messages[0].Content}, nil
	}
	return generate.Response{}, nil
}

func newRecorder(t *testing.T) (*otelrag.Wrapper, *tracetest.InMemoryExporter, *rag.System) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
	})
	w := otelrag.Wrap(sys, otelrag.Config{TracerProvider: tp})
	return w, exp, sys
}

func findSpan(t *testing.T, exp *tracetest.InMemoryExporter, name string) tracesdk.ReadOnlySpan {
	t.Helper()
	for _, s := range exp.GetSpans().Snapshots() {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("span %q not found; have %v", name, spanNames(exp))
	return nil
}

func spanNames(exp *tracetest.InMemoryExporter) []string {
	out := make([]string, 0)
	for _, s := range exp.GetSpans().Snapshots() {
		out = append(out, s.Name())
	}
	return out
}

func attrValue(s tracesdk.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.Emit(), true
		}
	}
	return "", false
}

func TestWrap_ImportEmitsSpan(t *testing.T) {
	w, exp, _ := newRecorder(t)
	_, err := w.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# Title\nhello"},
		{ID: "d2", Content: "# Title2\nworld"},
	}, ingest.ImportOptions{Namespace: "test"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	span := findSpan(t, exp, otelrag.OperationImport)
	if v, ok := attrValue(span, otelrag.AttrNamespace); !ok || v != "test" {
		t.Fatalf("namespace attr = %q (found=%v)", v, ok)
	}
	if v, ok := attrValue(span, otelrag.AttrImportDocs); !ok || v != "2" {
		t.Fatalf("documents attr = %q (found=%v)", v, ok)
	}
	if _, ok := attrValue(span, otelrag.AttrImportChunks); !ok {
		t.Fatalf("chunks attr missing")
	}
}

func TestWrap_RetrieveEmitsSpan(t *testing.T) {
	w, exp, _ := newRecorder(t)
	if _, err := w.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "paris france capital"},
	}, ingest.ImportOptions{Namespace: "test"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	exp.Reset()
	hits, err := w.Retrieve(context.Background(), "paris", rag.SearchOptions{Namespace: "test", TopK: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no hits returned")
	}
	span := findSpan(t, exp, otelrag.OperationRetrieve)
	if v, ok := attrValue(span, otelrag.AttrNamespace); !ok || v != "test" {
		t.Fatalf("namespace attr = %q (found=%v)", v, ok)
	}
	if v, ok := attrValue(span, otelrag.AttrTopK); !ok || v != "3" {
		t.Fatalf("top_k attr = %q (found=%v)", v, ok)
	}
	if _, ok := attrValue(span, otelrag.AttrHitCount); !ok {
		t.Fatalf("hit_count attr missing")
	}
}

func TestWrap_AskEmitsSpanWithRoutePolicy(t *testing.T) {
	w, exp, _ := newRecorder(t)
	if _, err := w.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# Cities\noverview\n## Travel\nmuseums and cafes in paris"},
	}, ingest.ImportOptions{Namespace: "test"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	exp.Reset()
	_, err := w.Ask(context.Background(), "paris museums", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "test", TopK: 2},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	span := findSpan(t, exp, otelrag.OperationAsk)
	if v, ok := attrValue(span, otelrag.AttrNamespace); !ok || v != "test" {
		t.Fatalf("namespace attr = %q (found=%v)", v, ok)
	}
	if _, ok := attrValue(span, otelrag.AttrHitCount); !ok {
		t.Fatalf("hit_count attr missing")
	}
}

func TestObserver_OnAskAddsEvent(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	obs := otelrag.Observer(otelrag.Config{TracerProvider: tp})
	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
		Observer: obs,
	})

	// Manually start a parent span so the observer's AddEvent calls land
	// somewhere observable.
	tracer := tp.Tracer("test")
	ctx, parent := tracer.Start(context.Background(), "parent")

	if _, err := sys.Import(ctx, []ingest.Document{
		{ID: "d1", Content: "# T\npastry"},
	}, ingest.ImportOptions{Namespace: "test"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := sys.Ask(ctx, "pastry", rag.AskOptions{Search: rag.SearchOptions{Namespace: "test", TopK: 1}}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	parent.End()

	span := findSpan(t, exp, "parent")
	events := span.Events()
	foundImport, foundAsk := false, false
	for _, ev := range events {
		switch ev.Name {
		case otelrag.OperationImport:
			foundImport = true
		case otelrag.OperationAsk:
			foundAsk = true
		}
	}
	if !foundImport {
		t.Fatalf("expected %q event on parent span; got %v", otelrag.OperationImport, eventNames(events))
	}
	if !foundAsk {
		t.Fatalf("expected %q event on parent span; got %v", otelrag.OperationAsk, eventNames(events))
	}
}

func eventNames(events []tracesdk.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Name)
	}
	return out
}

// --- metrics (RAG-OBS-02) ---

func newMetricWrapper(t *testing.T) (*otelrag.Wrapper, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
	})
	return otelrag.Wrap(sys, otelrag.Config{MeterProvider: mp}), reader
}

func metricNames(t *testing.T, reader *sdkmetric.ManualReader) map[string]bool {
	t.Helper()
	rm := &metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect(): %v", err)
	}
	seen := make(map[string]bool)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			seen[m.Name] = true
		}
	}
	return seen
}

func assertMetrics(t *testing.T, reader *sdkmetric.ManualReader, want ...string) {
	t.Helper()
	seen := metricNames(t, reader)
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("metric %q missing; saw %v", name, seen)
		}
	}
}

func TestWrap_AskEmitsREDAndTokenMetrics(t *testing.T) {
	w, reader := newMetricWrapper(t)
	if _, err := w.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# Cuisine\nFrench pastry recipes and croissants."},
	}, ingest.ImportOptions{Namespace: "test"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := w.Ask(context.Background(), "pastry", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "test", TopK: 1},
	}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	assertMetrics(t, reader,
		otelrag.MetricRequests, otelrag.MetricDuration, otelrag.MetricTokens)
}

func TestWrap_ErrorEmitsErrorMetric(t *testing.T) {
	w, reader := newMetricWrapper(t)
	// An empty query fails inside Retrieve → Ask returns an error.
	if _, err := w.Ask(context.Background(), "", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "test", TopK: 1},
	}); err == nil {
		t.Fatalf("Ask with empty query: want error")
	}
	assertMetrics(t, reader, otelrag.MetricRequests, otelrag.MetricErrors)
}

// --- OnGenerateUsage metric (RAG-OBS-03, v0.3.0) ---

// collectDataPoints returns the Int64 Sum data points of the named metric
// from a ManualReader collection. Fails the test if the metric is absent
// or has the wrong shape.
func collectDataPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	rm := &metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), rm); err != nil {
		t.Fatalf("Collect(): %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q is not Sum[int64]; got %T", name, m.Data)
			}
			return sum.DataPoints
		}
	}
	return nil
}

// findDataPoint finds the first data point whose attributes contain ALL of
// the given key/value pairs. Returns the data point and true on match.
func findDataPoint(dps []metricdata.DataPoint[int64], match map[string]string) (metricdata.DataPoint[int64], bool) {
	for _, dp := range dps {
		ok := true
		for wantKey, wantVal := range match {
			val, present := dp.Attributes.Value(attrKey(wantKey))
			if !present || val.Emit() != wantVal {
				ok = false
				break
			}
		}
		if ok {
			return dp, true
		}
	}
	return metricdata.DataPoint[int64]{}, false
}

func attrKey(k string) attribute.Key { return attribute.Key(k) }

func TestObserver_OnGenerateUsageEmitsPromptCompletionMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer := otelrag.Observer(otelrag.Config{MeterProvider: mp})
	if observer.OnGenerateUsage == nil {
		t.Fatalf("Observer().OnGenerateUsage is nil; want non-nil hook")
	}

	observer.OnGenerateUsage(context.Background(), "ask", obs.TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		Estimated:        false,
	})

	dps := collectDataPoints(t, reader, otelrag.MetricGenerateTokens)
	if len(dps) != 2 {
		t.Fatalf("got %d data points, want 2; dps=%+v", len(dps), dps)
	}
	prompt, ok := findDataPoint(dps, map[string]string{
		otelrag.AttrStage:     "ask",
		otelrag.AttrTokenKind: "prompt",
		otelrag.AttrEstimated: "false",
	})
	if !ok {
		t.Fatalf("prompt data point not found in %+v", dps)
	}
	if prompt.Value != 100 {
		t.Fatalf("prompt value = %d, want 100", prompt.Value)
	}
	completion, ok := findDataPoint(dps, map[string]string{
		otelrag.AttrStage:     "ask",
		otelrag.AttrTokenKind: "completion",
		otelrag.AttrEstimated: "false",
	})
	if !ok {
		t.Fatalf("completion data point not found in %+v", dps)
	}
	if completion.Value != 50 {
		t.Fatalf("completion value = %d, want 50", completion.Value)
	}
}

func TestObserver_OnGenerateUsageDropsZeroTokens(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer := otelrag.Observer(otelrag.Config{MeterProvider: mp})

	observer.OnGenerateUsage(context.Background(), "ask", obs.TokenUsage{
		PromptTokens:     0,
		CompletionTokens: 50,
	})

	dps := collectDataPoints(t, reader, otelrag.MetricGenerateTokens)
	if len(dps) != 1 {
		t.Fatalf("got %d data points, want 1 (only completion); dps=%+v", len(dps), dps)
	}
	if _, ok := findDataPoint(dps, map[string]string{
		otelrag.AttrTokenKind: "completion",
	}); !ok {
		t.Fatalf("expected completion data point in %+v", dps)
	}
	if _, ok := findDataPoint(dps, map[string]string{
		otelrag.AttrTokenKind: "prompt",
	}); ok {
		t.Fatalf("did not expect prompt data point; dps=%+v", dps)
	}
}

func TestObserver_OnGenerateUsagePreservesEstimatedFlag(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer := otelrag.Observer(otelrag.Config{MeterProvider: mp})

	observer.OnGenerateUsage(context.Background(), "grader", obs.TokenUsage{
		PromptTokens:     7,
		CompletionTokens: 3,
		Estimated:        true,
	})

	dps := collectDataPoints(t, reader, otelrag.MetricGenerateTokens)
	if len(dps) != 2 {
		t.Fatalf("got %d data points, want 2; dps=%+v", len(dps), dps)
	}
	if _, ok := findDataPoint(dps, map[string]string{
		otelrag.AttrStage:     "grader",
		otelrag.AttrTokenKind: "prompt",
		otelrag.AttrEstimated: "true",
	}); !ok {
		t.Fatalf("estimated=true prompt data point not found in %+v", dps)
	}
}

func TestWrap_NoMeterProviderIsNoopSafe(t *testing.T) {
	sys := rag.New(rag.Options{
		Model:    fakeModel{},
		Splitter: ingest.NewMarkdownSplitter(500, 50),
	})
	w := otelrag.Wrap(sys) // no Config → no-op meter
	if _, err := w.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "# T\ncontent"},
	}, ingest.ImportOptions{Namespace: "test"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := w.Ask(context.Background(), "content", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "test", TopK: 1},
	}); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	// No panic with a no-op meter = pass.
}
