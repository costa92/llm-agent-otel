package otelflow_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	v2flow "github.com/costa92/llm-agent-flow/v2/flow"
	otelflow "github.com/costa92/llm-agent-otel/otelflow/v2"

	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// resumableStub satisfies v2flow.ResumableRunner. RunResumable returns a
// canned Suspension; Resume returns canned Outputs.
type resumableStub struct {
	streamEvents []v2flow.Event
	suspendToken string
	resumeOut    map[string]any
}

func (s *resumableStub) Run(_ context.Context, _ map[string]any) (map[string]any, error) {
	return s.resumeOut, nil
}

func (s *resumableStub) RunStream(_ context.Context, _ map[string]any) (<-chan v2flow.Event, error) {
	ch := make(chan v2flow.Event, len(s.streamEvents))
	for _, ev := range s.streamEvents {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (s *resumableStub) RunResumable(_ context.Context, _ string, _ map[string]any) (v2flow.RunResult, error) {
	return v2flow.RunResult{Suspended: &v2flow.Suspension{
		ResumeToken: s.suspendToken,
		NodeID:      "approve",
		Request:     v2flow.InterruptRequest{Kind: "approval", Prompt: "ok?"},
	}}, nil
}

func (s *resumableStub) Resume(_ context.Context, _, _ string, _ map[string]any) (v2flow.RunResult, error) {
	return v2flow.RunResult{Outputs: s.resumeOut}, nil
}

// plainStub satisfies only v2flow.Runner (no resumable surface).
type plainStub struct{ streamEvents []v2flow.Event }

func (s *plainStub) Run(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (s *plainStub) RunStream(_ context.Context, _ map[string]any) (<-chan v2flow.Event, error) {
	ch := make(chan v2flow.Event, len(s.streamEvents))
	for _, ev := range s.streamEvents {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func newRecorder() (*tracetest.InMemoryExporter, *tracesdk.TracerProvider) {
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	return exp, tp
}

func TestWrap_ResumableInnerExposesCapability(t *testing.T) {
	_, tp := newRecorder()
	w := otelflow.Wrap(&resumableStub{}, otelflow.Config{TracerProvider: tp})
	if _, ok := w.(v2flow.ResumableRunner); !ok {
		t.Fatal("Wrap over a ResumableRunner did not expose ResumableRunner")
	}
}

func TestWrap_NonResumableInner_NoFalsePositive(t *testing.T) {
	_, tp := newRecorder()
	w := otelflow.Wrap(&plainStub{}, otelflow.Config{TracerProvider: tp})
	if _, ok := w.(v2flow.ResumableRunner); ok {
		t.Fatal("Wrap over a non-resumable Runner falsely exposed ResumableRunner")
	}
}

func TestWrap_ResumeEmitsSpanPair(t *testing.T) {
	exp, tp := newRecorder()
	const token = "tok-123"
	stub := &resumableStub{suspendToken: token, resumeOut: map[string]any{"out": "x"}}
	w := otelflow.Wrap(stub, otelflow.Config{TracerProvider: tp, FlowID: "f"})
	rr, ok := w.(v2flow.ResumableRunner)
	if !ok {
		t.Fatal("not resumable")
	}

	res, err := rr.RunResumable(context.Background(), "run1", map[string]any{"in": 1})
	if err != nil {
		t.Fatalf("RunResumable: %v", err)
	}
	if res.Suspended == nil || res.Suspended.ResumeToken != token {
		t.Fatalf("expected suspension with token %q, got %+v", token, res)
	}
	if _, err := rr.Resume(context.Background(), "run1", token, map[string]any{"approved": true}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	var sawSuspend, sawResume bool
	var resumeToken string
	for _, sp := range exp.GetSpans() {
		switch sp.Name {
		case "flow.suspend f":
			sawSuspend = true
		case "flow.resume f":
			sawResume = true
			for _, a := range sp.Attributes {
				if string(a.Key) == otelflow.AttrResumeToken {
					resumeToken = a.Value.AsString()
				}
			}
		}
	}
	if !sawSuspend {
		t.Fatalf("missing suspend span (names=%v)", spanNames(exp))
	}
	if !sawResume {
		t.Fatalf("missing resume span (names=%v)", spanNames(exp))
	}
	if resumeToken != token {
		t.Fatalf("resume span AttrResumeToken = %q, want %q", resumeToken, token)
	}
}

func TestWrap_RunStreamSpansBalance(t *testing.T) {
	exp, tp := newRecorder()
	stub := &plainStub{streamEvents: []v2flow.Event{
		{Kind: v2flow.FlowStarted, FlowID: "f"},
		{Kind: v2flow.NodeStarted, NodeID: "a"},
		{Kind: v2flow.NodeFinished, NodeID: "a", Output: map[string]any{"o": 1}},
		{Kind: v2flow.FlowDone, Outputs: map[string]any{"out": "x"}},
	}}
	w := otelflow.Wrap(stub, otelflow.Config{TracerProvider: tp, FlowID: "f"})

	before := runtime.NumGoroutine()
	ch, err := w.RunStream(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for range ch {
	}
	// Let the consume goroutine fully unwind after channel close.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2 (root + node a) names=%v", len(spans), spanNames(exp))
	}
	var sawRoot, sawNode bool
	for _, sp := range spans {
		switch sp.Name {
		case "flow.run.stream f":
			sawRoot = true
		case "flow.node a":
			sawNode = true
			mustHaveAttr(t, sp.Attributes, otelflow.AttrNodeID, "a")
		}
		if sp.EndTime.IsZero() {
			t.Fatalf("span %q was not Ended", sp.Name)
		}
	}
	if !sawRoot || !sawNode {
		t.Fatalf("missing root or node span (names=%v)", spanNames(exp))
	}
}

func mustHaveAttr(t *testing.T, attrs []attribute.KeyValue, key, want string) {
	t.Helper()
	for _, a := range attrs {
		if string(a.Key) == key {
			if a.Value.AsString() != want {
				t.Fatalf("attr %s = %q, want %q", key, a.Value.AsString(), want)
			}
			return
		}
	}
	t.Fatalf("missing attribute %q (have %+v)", key, attrs)
}

func spanNames(exp *tracetest.InMemoryExporter) []string {
	spans := exp.GetSpans()
	out := make([]string, 0, len(spans))
	for _, sp := range spans {
		out = append(out, sp.Name)
	}
	return out
}
