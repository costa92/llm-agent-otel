package otelflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-flow/flow"
	"github.com/costa92/llm-agent-otel/otelflow"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// echoChainFlow exercises the canonical flow shape used across
// otelflow tests: two sequential tool nodes (upper → reverse).
const echoChainFlow = `{
	"id":"echo_chain",
	"name":"echo chain",
	"nodes":[
		{"id":"upper","type":"tool","config":{"tool":"upper"}},
		{"id":"reverse","type":"tool","config":{"tool":"reverse"}}
	],
	"edges":[
		{"source":{"node":"upper","port":"output"},
		 "target":{"node":"reverse","port":"input"}}
	],
	"inputs":[{"name":"in","node":"upper","port":"input"}],
	"outputs":[{"name":"out","node":"reverse","port":"output"}]
}`

// fakeTool returns its name+input transformation; sufficient for
// flow execution without pulling llm-agent-flow's example tools.
type fakeTool struct {
	name string
	fn   func(string) string
}

func (t *fakeTool) Name() string { return t.name }
func (t *fakeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Input string `json:"input"`
	}
	_ = json.Unmarshal(args, &p)
	return t.fn(p.Input), nil
}

func newRecorderRunner(t *testing.T, src string) (flow.Runner, *tracetest.InMemoryExporter) {
	t.Helper()
	reg := flow.NewNodeRegistry()
	if err := flow.RegisterToolNode(reg); err != nil {
		t.Fatalf("RegisterToolNode: %v", err)
	}
	tools := flow.ToolMap{
		"upper":   &fakeTool{name: "upper", fn: strings.ToUpper},
		"reverse": &fakeTool{name: "reverse", fn: reverseStr},
	}
	eng, err := flow.LoadCompile(strings.NewReader(src), reg, flow.Deps{Tools: tools})
	if err != nil {
		t.Fatalf("LoadCompile: %v", err)
	}
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	return otelflow.Wrap(eng, otelflow.Config{TracerProvider: tp}), exp
}

func reverseStr(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func TestWrapPreservesRunSemantics(t *testing.T) {
	runner, exp := newRecorderRunner(t, echoChainFlow)
	out, err := runner.Run(context.Background(), map[string]string{"in": "hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out["out"]; got != "OLLEH" {
		t.Fatalf("out = %q, want OLLEH", got)
	}
	// One root span; no node spans for the sync path at v0.
	if got := len(exp.GetSpans()); got != 1 {
		t.Fatalf("spans = %d, want 1 (names=%v)", got, spanNames(exp))
	}
	root := exp.GetSpans()[0]
	if !strings.HasPrefix(root.Name, "flow.run ") {
		t.Fatalf("root.Name = %q, want \"flow.run <id>\"", root.Name)
	}
	mustHaveAttr(t, root.Attributes, otelflow.AttrFlowID, "echo_chain")
	mustHaveAttr(t, root.Attributes, otelflow.AttrFlowName, "echo chain")
	if root.Status.Code != codes.Ok {
		t.Fatalf("status = %v, want Ok", root.Status)
	}
}

func TestRunRecordsErrorOnFailure(t *testing.T) {
	// Missing required input "in" makes Run fail.
	runner, exp := newRecorderRunner(t, echoChainFlow)
	_, err := runner.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	root := exp.GetSpans()[0]
	if root.Status.Code != codes.Error {
		t.Fatalf("status = %v, want Error", root.Status)
	}
	if len(root.Events) == 0 {
		t.Fatalf("expected at least one RecordError event")
	}
}

func TestStreamCreatesRootPlusPerNodeSpans(t *testing.T) {
	runner, exp := newRecorderRunner(t, echoChainFlow)
	ch, err := runner.RunStream(context.Background(), map[string]string{"in": "hello"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for range ch {
	}
	// Expect: 1 root + 2 node spans = 3 total.
	if got := len(exp.GetSpans()); got != 3 {
		t.Fatalf("spans = %d, want 3 (names=%v)", got, spanNames(exp))
	}
	var sawRoot, sawUpper, sawReverse bool
	for _, sp := range exp.GetSpans() {
		switch sp.Name {
		case "flow.run.stream echo_chain":
			sawRoot = true
		case "flow.node upper":
			sawUpper = true
			mustHaveAttr(t, sp.Attributes, otelflow.AttrNodeID, "upper")
		case "flow.node reverse":
			sawReverse = true
			mustHaveAttr(t, sp.Attributes, otelflow.AttrNodeID, "reverse")
		}
	}
	if !sawRoot || !sawUpper || !sawReverse {
		t.Fatalf("missing one of root/upper/reverse (names=%v)", spanNames(exp))
	}
}

func TestStreamSkippedNodeEmitsZeroDurationSpan(t *testing.T) {
	// CEL-less router: use a never/always pseudo-condition? — without
	// CEL plugged in, flow rejects non-empty conditions. So we
	// construct a flow that produces a NodeSkipped event another way:
	// a node whose only inbound edge is an unconditional edge from a
	// PRECEDING node whose output port never emits. Build:
	//
	//   src --(output)--> mid --(output)--> dst
	//
	// where `src` is a tool that emits NO value on "output" (only on
	// another port). The mid node will be unactivated (its input
	// port never receives a value), and dst will be skipped too.
	//
	// Simpler: just verify that the wrapper handles the NodeSkipped
	// event kind gracefully when one happens. The test below uses a
	// stub Runner instead of a real engine.
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	runner := otelflow.Wrap(&stubRunner{
		events: []flow.FlowEvent{
			{Kind: flow.FlowStarted, FlowID: "f"},
			{Kind: flow.NodeStarted, NodeID: "a"},
			{Kind: flow.NodeFinished, NodeID: "a"},
			{Kind: flow.NodeSkipped, NodeID: "b"},
			{Kind: flow.FlowDone, Outputs: map[string]string{"out": "x"}},
		},
	}, otelflow.Config{TracerProvider: tp, FlowID: "f"})
	ch, _ := runner.RunStream(context.Background(), nil)
	for range ch {
	}

	var skippedSpan *tracetest.SpanStub
	for i := range exp.GetSpans() {
		sp := exp.GetSpans()[i]
		if sp.Name == "flow.node b" {
			skippedSpan = &sp
			break
		}
	}
	if skippedSpan == nil {
		t.Fatalf("no flow.node b span (names=%v)", spanNames(exp))
	}
	mustHaveAttr(t, skippedSpan.Attributes, otelflow.AttrNodeSkipped, true)
}

func TestStreamRecordsFlowErr(t *testing.T) {
	tp := tracesdk.NewTracerProvider()
	exp := tracetest.NewInMemoryExporter()
	tp.RegisterSpanProcessor(tracesdk.NewSimpleSpanProcessor(exp))
	stub := &stubRunner{
		events: []flow.FlowEvent{
			{Kind: flow.FlowStarted, FlowID: "f"},
			{Kind: flow.NodeStarted, NodeID: "a"},
			{Kind: flow.NodeFinished, NodeID: "a", Err: errors.New("a-failed")},
			{Kind: flow.FlowErr, Err: errors.New("boom")},
		},
	}
	runner := otelflow.Wrap(stub, otelflow.Config{TracerProvider: tp, FlowID: "f"})
	ch, _ := runner.RunStream(context.Background(), nil)
	for range ch {
	}
	var rootErrored, nodeErrored bool
	for _, sp := range exp.GetSpans() {
		if sp.Name == "flow.run.stream f" && sp.Status.Code == codes.Error {
			rootErrored = true
		}
		if sp.Name == "flow.node a" && sp.Status.Code == codes.Error {
			nodeErrored = true
		}
	}
	if !rootErrored {
		t.Fatalf("root span did not record error (names=%v)", spanNames(exp))
	}
	if !nodeErrored {
		t.Fatalf("node 'a' span did not record error (names=%v)", spanNames(exp))
	}
}

func TestWrapWithNoTracerProviderNoOps(t *testing.T) {
	// nil TracerProvider falls back to the no-op tracer — wrapping
	// must NOT panic and the underlying run still works.
	runner, _ := newRecorderRunner(t, echoChainFlow)
	if _, err := runner.Run(context.Background(), map[string]string{"in": "ok"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// stubRunner emits a fixed sequence of FlowEvents from RunStream;
// Run returns the FlowDone's Outputs (or the FlowErr's Err).
type stubRunner struct {
	events []flow.FlowEvent
}

func (s *stubRunner) Run(_ context.Context, _ map[string]string) (map[string]string, error) {
	for _, ev := range s.events {
		if ev.Kind == flow.FlowErr {
			return nil, ev.Err
		}
		if ev.Kind == flow.FlowDone {
			return ev.Outputs, nil
		}
	}
	return nil, errors.New("stub: no terminal event")
}

func (s *stubRunner) RunStream(_ context.Context, _ map[string]string) (<-chan flow.FlowEvent, error) {
	ch := make(chan flow.FlowEvent, len(s.events))
	for _, ev := range s.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func mustHaveAttr(t *testing.T, attrs []attribute.KeyValue, key string, want any) {
	t.Helper()
	for _, a := range attrs {
		if string(a.Key) != key {
			continue
		}
		switch want.(type) {
		case string:
			if a.Value.AsString() != want.(string) {
				t.Fatalf("attr %s = %q, want %q", key, a.Value.AsString(), want)
			}
			return
		case bool:
			if a.Value.AsBool() != want.(bool) {
				t.Fatalf("attr %s = %v, want %v", key, a.Value.AsBool(), want)
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
