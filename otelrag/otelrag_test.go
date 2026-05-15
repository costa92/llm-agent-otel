package otelrag_test

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-otel/otelrag"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"

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
